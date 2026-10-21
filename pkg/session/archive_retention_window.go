package session

import (
	"fmt"
)

// projectRetentionWindowLocked derives the window from committed exact row
// tuples. A missing unrelated live row is never converted into expiry. Both
// the backend mutex and archive mutex are held by runtime callers.
func (s *ArchiveDayStore) projectRetentionWindowLocked(meta archiveBackendMeta, state savedInputLifecycle) (archiveBackendMeta, windowExpiry, error) {
	n, err := s.ordinalCountLocked()
	if err != nil {
		return meta, windowExpiry{}, err
	}
	if meta.Skip < 0 || meta.Skip > n || meta.Count < 0 || meta.Count > n || meta.ClearFloor < 0 || meta.ClearFloor > meta.Skip {
		return meta, windowExpiry{}, fmt.Errorf("retention: publication history disagrees with the saved window")
	}
	lastRetired := -1
	for from := meta.Skip; from < n; from += scanChunk {
		rows, err := s.readOrdinalRowsRepairedLocked(from, min(n, from+scanChunk))
		if err != nil {
			return meta, windowExpiry{}, err
		}
		for _, row := range rows {
			if retractedOrdinal(meta.Retracted, row.Ordinal) {
				continue
			}
			if state.rowRetired(row) {
				lastRetired = row.Ordinal
				continue
			}
			if err := s.validatePublishedRowLocked(row); err != nil {
				return meta, windowExpiry{}, fmt.Errorf("retention: live publication row %d has no committed expiry: %w", row.Ordinal, err)
			}
		}
	}
	newSkip := max(meta.Skip, lastRetired+1)
	for newSkip < n && newSkip > meta.Skip {
		rows, err := s.readOrdinalRowsRepairedLocked(newSkip, newSkip+1)
		if err != nil {
			return meta, windowExpiry{}, err
		}
		if rows[0].Role != "tool" {
			break
		}
		newSkip++ // The same complete-group rule as the canonical expiry pass.
	}
	if anchor := meta.AnchorLine; anchor != nil {
		if *anchor < 0 || *anchor >= meta.Skip || *anchor >= n {
			return meta, windowExpiry{}, fmt.Errorf("retention: invalid retained user anchor")
		}
		rows, err := s.readOrdinalRowsRepairedLocked(*anchor, *anchor+1)
		if err != nil {
			return meta, windowExpiry{}, err
		}
		if rows[0].Role != "user" {
			return meta, windowExpiry{}, fmt.Errorf("retention: invalid retained user anchor")
		}
		if state.rowRetired(rows[0]) {
			meta.AnchorLine = nil
		} else if !retractedOrdinal(meta.Retracted, *anchor) {
			if err := s.validatePublishedRowLocked(rows[0]); err != nil {
				return meta, windowExpiry{}, fmt.Errorf("retention: required anchor has no committed expiry: %w", err)
			}
		}
	}
	out := windowExpiry{}
	if newSkip > meta.Skip {
		out = windowExpiry{moved: true, emptied: newSkip == n, note: expiryNoteText(newSkip, n)}
		if newSkip < n {
			rows, err := s.readOrdinalRowsRepairedLocked(newSkip, newSkip+1)
			if err != nil {
				return meta, windowExpiry{}, err
			}
			addr := rows[0].Slot
			out.first = &addr
		}
		meta.ClearFloor = max(meta.ClearFloor, newSkip)
		meta.ExpiryNote = out.note
	}
	meta.Skip, meta.Count = newSkip, n
	return meta, out, nil
}
