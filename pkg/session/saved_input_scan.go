package session

import (
	"fmt"
	"os"
)

// scanSavedInputIntegrity must see every identity/consumption record before it
// can certify that a report is absent or unconsumed. Recall's tolerant scanners
// are deliberately unchanged: a skipped corrupt row is useful for recall but
// cannot establish the negative fact that makes a saved input pending again.
func (s *ArchiveDayStore) scanSavedInputIntegrity(pending savedInputIndex, fn func(ArchiveAddress, ArchiveRecord) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.scanAllLocked(fn, true); err != nil {
		return err
	}
	return s.checkMissingCurrentPublicationLocked(pending)
}

// checkMissingCurrentPublicationLocked validates actual record locations, not
// the current filename alone. A rename may precede marker publication, and an
// intact file may still be truncated or hold the wrong record. Only required
// addresses are validated; an uncreated, unreferenced new partition is harmless.
// The caller holds the archive lock; evidence failures never mean unconsumed.
func (s *ArchiveDayStore) checkMissingCurrentPublicationLocked(pending savedInputIndex) error {
	mark, err := s.readDayMarkLocked()
	if err != nil {
		return err
	}
	for id, entry := range pending {
		_, rec, err := s.readAtRawLocked(entry.Address)
		if err == nil {
			err = rec.Validate()
		}
		if err != nil {
			return fmt.Errorf("saved input integrity: current partition %q: pending input %q: %w", mark, id, err)
		}
		if rec.ID != id || rec.ViewMembership != ViewMembershipChat || rec.ModelMessage == nil {
			return fmt.Errorf("saved input integrity: current partition %q: pending input %q is not its saved payload", mark, id)
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
	var offset int64
	for ordinal := 0; ordinal < n; ordinal++ {
		row, size, readErr := readRowAt(rows, offset)
		if readErr != nil {
			return fmt.Errorf("saved input integrity: current partition %q: read publication row %d: %w", mark, ordinal, readErr)
		}
		if row.Ordinal != ordinal {
			return fmt.Errorf("saved input integrity: current partition %q: expected publication row %d, found %d", mark, ordinal, row.Ordinal)
		}
		if err := s.validatePublishedRowLocked(row); err != nil {
			return fmt.Errorf("saved input integrity: current partition %q: publication row %d: %w", mark, ordinal, err)
		}
		offset += int64(size)
	}
	return nil
}

// validatePublishedRowLocked checks both the placement and its source through
// the same bounded reader as public addressed reads. It uses constant per-row
// memory and never infers consumption from an incomplete partition scan.
func (s *ArchiveDayStore) validatePublishedRowLocked(row ordinalRow) error {
	_, slot, err := s.readAtRawLocked(row.Slot)
	if err != nil {
		return err
	}
	if err := slot.Validate(); err != nil {
		return err
	}
	if row.Source == nil {
		if slot.ModelRef != nil || !slot.holdsModelSlot() {
			return fmt.Errorf("published slot %q disagrees with its ordinal row", row.Slot.EntryID)
		}
		return nil
	}
	ref, ok := slot.ReferenceAddress()
	if !ok || ref != *row.Source {
		return fmt.Errorf("published placement %q disagrees with its source address", row.Slot.EntryID)
	}
	_, source, err := s.readAtRawLocked(*row.Source)
	if err != nil {
		return err
	}
	if err := source.Validate(); err != nil {
		return err
	}
	if source.ModelMessage == nil {
		return fmt.Errorf("published source %q has no model payload", row.Source.EntryID)
	}
	return nil
}
