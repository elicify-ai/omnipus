package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// scanSavedInputIntegrity must see every identity/consumption record before it
// can certify that a report is absent or unconsumed. Recall's tolerant scanners
// are deliberately unchanged: a skipped corrupt row is useful for recall but
// cannot establish the negative fact that makes a saved input pending again.
func (s *ArchiveDayStore) scanSavedInputIntegrity(pending savedInputIndex, fn func(ArchiveAddress, ArchiveRecord) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMissingCurrentPublicationLocked(pending); err != nil {
		return err
	}
	return s.scanAllLocked(fn, true)
}

// checkMissingCurrentPublicationLocked distinguishes an uncreated rollover file
// from loss proven by the existing pending/ordinal addresses. The caller holds
// the archive lock. Evidence-read failures are errors, never proof of absence.
func (s *ArchiveDayStore) checkMissingCurrentPublicationLocked(pending savedInputIndex) error {
	mark, err := s.readDayMarkLocked()
	if err != nil {
		return err
	}
	if mark == "" {
		return nil
	}
	_, missingErr := os.Stat(filepath.Join(s.dir(), archiveCurrentFile))
	if missingErr == nil {
		return nil
	}
	if !errors.Is(missingErr, fs.ErrNotExist) {
		return fmt.Errorf("saved input integrity: stat current partition %q: %w", mark, missingErr)
	}
	for id, entry := range pending {
		if entry.Address.PartitionKey == mark {
			return fmt.Errorf("saved input integrity: current partition %q is missing but pending input %q addresses it: %w", mark, id, missingErr)
		}
	}
	n, err := s.ordinalCountLocked()
	if err != nil {
		return fmt.Errorf("saved input integrity: current partition %q: read publication index: %w", mark, err)
	}
	if n == 0 {
		return nil
	}
	rows, err := os.Open(s.ordinalRowsPath())
	if err != nil {
		return fmt.Errorf("saved input integrity: current partition %q: open publication rows: %w", mark, err)
	}
	defer rows.Close()
	// Inspect bounded rows one at a time, including Slot (the placement), not
	// just Source (which may survive in an older partition). This exceptional
	// missing-file path allocates no lifetime-sized slice.
	var offset int64
	for ordinal := 0; ordinal < n; ordinal++ {
		row, size, readErr := readRowAt(rows, offset)
		if readErr != nil {
			return fmt.Errorf("saved input integrity: current partition %q: read publication row %d: %w", mark, ordinal, readErr)
		}
		if row.Ordinal != ordinal {
			return fmt.Errorf("saved input integrity: current partition %q: expected publication row %d, found %d", mark, ordinal, row.Ordinal)
		}
		if row.Slot.PartitionKey == mark || (row.Source != nil && row.Source.PartitionKey == mark) {
			return fmt.Errorf("saved input integrity: current partition %q is missing but ordinal %d publishes record %q: %w", mark, ordinal, row.Slot.EntryID, missingErr)
		}
		offset += int64(size)
	}
	return nil
}
