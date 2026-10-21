package session

import "fmt"

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
	meta, err := loadArchiveBackendMeta(s.dir())
	if err != nil {
		return fmt.Errorf("saved input integrity: read retained window: %w", err)
	}
	state, err := loadSavedInputLifecycle(s.dir(), s.sessionID)
	if err != nil {
		return err
	}
	if len(state.Deletions) > 0 {
		meta, _, err = s.projectRetentionWindowLocked(meta, state)
		if err != nil {
			return err
		}
	}
	if meta.Skip < 0 || meta.Skip > n || meta.Count < 0 || meta.Count > n || meta.ClearFloor < 0 || meta.ClearFloor > meta.Skip {
		return fmt.Errorf("saved input integrity: invalid retained window cursor")
	}
	// Retention intentionally keeps ordinal metadata after removing old bytes.
	// Clear/trim move this boundary, and rollback excludes retained spans. None
	// of those rows is required live content. A live reference's older Source
	// remains required and is checked by validatePublishedRowLocked.
	for from := meta.Skip; from < n; from += scanChunk {
		rows, err := s.readOrdinalRowsRepairedLocked(from, min(n, from+scanChunk))
		if err != nil {
			return fmt.Errorf("saved input integrity: current partition %q: read publication rows: %w", mark, err)
		}
		for _, row := range rows {
			if retractedOrdinal(meta.Retracted, row.Ordinal) {
				continue
			}
			if err := s.validatePublishedRowLocked(row); err != nil {
				return fmt.Errorf("saved input integrity: current partition %q: publication row %d: %w", mark, row.Ordinal, err)
			}
		}
	}
	if a := meta.AnchorLine; a != nil {
		if *a < 0 || *a >= meta.Skip || *a >= n {
			return fmt.Errorf("saved input integrity: invalid retained user anchor")
		}
		rows, err := s.readOrdinalRowsRepairedLocked(*a, *a+1)
		if err != nil {
			return fmt.Errorf("saved input integrity: read retained user anchor: %w", err)
		}
		if len(rows) != 1 || rows[0].Role != "user" {
			return fmt.Errorf("saved input integrity: invalid retained user anchor")
		}
		if !retractedOrdinal(meta.Retracted, *a) {
			if err := s.validatePublishedRowLocked(rows[0]); err != nil {
				return fmt.Errorf("saved input integrity: retained user anchor %d: %w", *a, err)
			}
		}
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
