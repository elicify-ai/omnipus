package session

import (
	"fmt"
	"strings"
)

func (s savedInputLifecycle) inputWasConsumed(source ArchiveAddress) bool {
	if p, ok := s.PollConsumed[source.EntryID]; ok && (p.Source == nil || *p.Source == source) {
		return true
	}
	for _, notice := range s.Notices {
		if notice.ModelAdmitted && notice.Source != nil && *notice.Source == source {
			return true
		}
	}
	for _, d := range s.Deletions {
		for _, row := range d.RetiredModelRows {
			if row.Source != nil && *row.Source == source {
				return true
			}
		}
	}
	return false
}

func (s savedInputLifecycle) rowRetired(row ordinalRow) bool {
	for _, d := range s.Deletions {
		for _, retired := range d.RetiredModelRows {
			if retired.Ordinal == row.Ordinal && retired.Slot == row.Slot && sameInputSource(retired.Source, row.Source) {
				return true
			}
		}
	}
	return false
}

func sameInputSource(a, b *ArchiveAddress) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// Only a newly committed, not-yet-unlinked incarnation can classify a pending
// source. Completed old day-key receipts never select a later replacement.
func (s savedInputLifecycle) deletionForAddress(a ArchiveAddress) (savedInputDeletion, bool) {
	for _, d := range s.Deletions {
		if !d.Unlinked && d.PartitionKey == a.PartitionKey && a.ByteOffset < d.SizeBytes {
			return d, true
		}
	}
	return savedInputDeletion{}, false
}

func (s savedInputLifecycle) isNoticeInput(id string) bool {
	for _, e := range s.ExpiredReports {
		if e.NoticeID == id {
			return true
		}
	}
	return false
}

func (s savedInputLifecycle) retiredNoticeSource(a ArchiveAddress) bool {
	for _, n := range s.Notices {
		if n.Source == nil || *n.Source != a {
			continue
		}
		for _, d := range s.Deletions {
			for _, retired := range d.RetiredNoticeSources {
				if retired == a {
					return true
				}
			}
		}
	}
	return false
}

func (s savedInputLifecycle) pendingProjection(idx savedInputIndex) (savedInputIndex, error) {
	out := make(savedInputIndex, len(idx))
	for id, entry := range idx {
		if e, ok := s.ExpiredReports[id]; ok {
			if e.Source != entry.Address {
				return nil, fmt.Errorf("saved input %q conflicts with its exact expiry receipt", id)
			}
			continue
		}
		if p, ok := s.PollConsumed[id]; ok && p.Source != nil && *p.Source != entry.Address {
			return nil, fmt.Errorf("saved input %q conflicts with its poll receipt", id)
		}
		if s.inputWasConsumed(entry.Address) || s.retiredNoticeSource(entry.Address) {
			continue
		}
		out[id] = entry
	}
	return out, nil
}

// promotePolledInputsLocked retains an existing exact server marker's fact
// before age-based unlink. It never fabricates consumption from an absent row.
func (s *ArchiveDayStore) promotePolledInputsLocked(state *savedInputLifecycle, hasPending bool) error {
	requireStrict := hasPending || len(state.Notices) > 0 || len(state.PollConsumed) > 0
	sources := map[string]ArchiveAddress{}
	markers := map[string]struct {
		addr ArchiveAddress
		rec  ArchiveRecord
	}{}
	var noticeErr error
	if err := s.scanAllLocked(func(addr ArchiveAddress, rec ArchiveRecord) bool {
		if state.isNoticeInput(rec.ID) {
			for receiptID, notice := range state.Notices {
				e, _ := state.ExpiredReportsByReceipt(receiptID)
				if e.NoticeID != rec.ID {
					continue
				}
				if rec.Type != EntryTypeSystem || rec.Role != "system" || rec.SystemSubtype != SystemSubtypeExpiryNotice || rec.Content != savedInputExpiryCopy(s.sessionID, e) || rec.ModelMessage == nil {
					noticeErr = fmt.Errorf("retention notice %q conflicts with its receipt", rec.ID)
					return false
				}
				if notice.Source != nil && *notice.Source != addr {
					noticeErr = fmt.Errorf("retention notice %q has conflicting immutable sources", rec.ID)
					return false
				}
				notice.Source, notice.ChatSaved = &addr, true
				state.Notices[receiptID] = notice
			}
		}
		if rec.ViewMembership == ViewMembershipChat && rec.ModelMessage != nil && !state.isNoticeInput(rec.ID) {
			requireStrict = true
			sources[rec.ID] = addr
		}
		if rest, ok := strings.CutPrefix(rec.ID, "consumed-"); ok && rest != "" && rec.Type == EntryTypeSystem && rec.Role == "system" && rec.Content == "consumed "+rest {
			markers["saved-input-"+rest] = struct {
				addr ArchiveAddress
				rec  ArchiveRecord
			}{addr, rec}
		}
		return true
	}, false); err != nil {
		return err
	}
	if requireStrict {
		// Tolerant discovery above copies only positive markers. Any saved-input
		// ownership/source requires the full strict consumption-evidence scan;
		// an unrelated legacy transcript with no such state is ordinary expiry.
		if err := s.scanAllLocked(func(ArchiveAddress, ArchiveRecord) bool { return true }, true); err != nil {
			return err
		}
	}
	if noticeErr != nil {
		return noticeErr
	}
	for id, marker := range markers {
		if _, prefixedSource := sources[id]; !prefixedSource {
			unprefixed := strings.TrimPrefix(id, "saved-input-")
			if _, ok := sources[unprefixed]; ok {
				id = unprefixed
			}
		}
		if _, exists := state.PollConsumed[id]; exists {
			continue
		}
		if _, expired := state.ExpiredReports[id]; expired {
			return fmt.Errorf("saved input %q has conflicting expiry and consumed marker evidence", id)
		}
		if marker.rec.Timestamp.IsZero() {
			return fmt.Errorf("saved input %q: consumed marker has no recorded time", id)
		}
		poll := savedInputPoll{InputID: id, ConsumedAt: marker.rec.Timestamp.UTC(), MarkerID: marker.rec.ID, MarkerAddress: &marker.addr}
		if source, ok := sources[id]; ok {
			poll.Source = &source
		}
		state.PollConsumed[id] = poll
	}
	return nil
}

// captureRetiredNoticeSourcesLocked binds control-copy retirement to exact
// existing records, not a reused current filename or a numerical byte range.
func (s *ArchiveDayStore) captureRetiredNoticeSourcesLocked(state *savedInputLifecycle) error {
	for id, deletion := range state.Deletions {
		if deletion.Unlinked {
			continue
		}
		for _, notice := range state.Notices {
			if notice.Source == nil || notice.Source.PartitionKey != deletion.PartitionKey || state.retiredNoticeSource(*notice.Source) {
				continue
			}
			_, rec, err := s.readAtRawLocked(*notice.Source)
			if err != nil {
				return err
			}
			e, _ := state.ExpiredReportsByReceipt(notice.ReceiptID)
			if rec.ID != e.NoticeID || rec.Content != savedInputExpiryCopy(s.sessionID, e) || rec.Type != EntryTypeSystem || rec.Role != "system" {
				return fmt.Errorf("retention notice %q disagrees with its exact source", e.NoticeID)
			}
			deletion.RetiredNoticeSources = append(deletion.RetiredNoticeSources, *notice.Source)
		}
		state.Deletions[id] = deletion
	}
	return nil
}
