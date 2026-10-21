// archive_expiry.go: the FR-007 expiry/repair pass over the addressed archive —
// session-core C-ARCHIVE / U2 (spec FR-007, BDD-02.3).
//
// FR-007: "Retention MUST keep 90-day file-modification-age/default/disabled
// semantics and expire old main content despite protected identity. Expired mark
// advances to first retained complete group with agent expiry notice, or empty
// same-ID window when no complete content remains."
//
// The archive expires old day partitions and repairs the content-free
// AddressedWindow afterwards. It operates only on a session's own directory
// <sessionDir>/ (the rolled <day>.jsonl partitions) and its persisted window.json.
//
// Semantics kept from the existing session retention sweep
// (pkg/session/retention_sweep.go::RetentionSweep), so the two stay consistent:
//
//   - retentionDays <= 0 means retention is DISABLED — a no-op, zero deletions,
//     zero repair (the existing sweep's exact rule).
//   - the cutoff is now - retentionDays*24h, compared against each rolled
//     partition's file mtime, STRICTLY older-than: a file whose mtime equals the
//     cutoff is retained (BDD-02.3 B01/B05/B06 older/equal/younger fixtures).
//
// Only ROLLED partitions (<key>.jsonl) are candidates. The CURRENT partition
// (current.jsonl) is never deleted here: it is the live append target the day
// mark names, and the existing session sweep already deletes the whole session
// directory once the session itself ages out. A mark that pointed into a swept
// partition no longer resolves, which is exactly what the repair pass advances
// past.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// ExpiryNotice is the AGENT-VISIBLE outcome of one SweepExpired pass (FR-007:
// "...with agent expiry notice"). It is a plain value, not a wire type: the
// caller turns it into the session's ordinary notice surface. Expired is false
// for a no-op pass (retention disabled or nothing aged out).
type ExpiryNotice struct {
	// Expired is true when this pass removed at least one partition or advanced
	// the window past expired content.
	Expired bool `json:"expired"`
	// RemovedPartitions is the count of rolled partitions deleted this pass.
	RemovedPartitions int `json:"removed_partitions"`
	// WindowEmptied is true when no complete content remains, so the window is
	// now an empty same-ID window (the session identity is unchanged).
	WindowEmptied bool `json:"window_emptied"`
	// FirstRetained names the first retained model slot after repair, when any
	// content survives. Nil when the window was emptied or nothing changed.
	FirstRetained *ArchiveAddress `json:"first_retained,omitempty"`
	// Note is a short human-readable summary for the agent's expiry notice.
	Note string `json:"note,omitempty"`
}

// SweepExpired expires old rolled day partitions by file-modification age and
// repairs this session's persisted AddressedWindow so its Start mark advances to
// the first retained COMPLETE group — or the window is emptied (same session ID)
// when no complete content remains. It returns the agent-visible ExpiryNotice.
//
// A retentionDays <= 0 is the disabled case: it returns a non-expired no-op
// notice and touches nothing. This mirrors
// pkg/session/retention_sweep.go::RetentionSweep exactly, so a disabled install
// deletes nothing and manufactures no repair.
func (s *ArchiveDayStore) SweepExpired(retentionDays int) (ExpiryNotice, error) {
	return s.sweepExpiredWithCurrent(retentionDays, nil)
}

// Only unified retention supplies a current-file cutoff, after applying its
// existing protected-main policy. Standalone SweepExpired remains rolled-only.
func (s *ArchiveDayStore) sweepExpiredWithCurrent(retentionDays int, currentCutoff *time.Time) (ExpiryNotice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var removed int
	var err error
	if currentCutoff == nil {
		removed, err = s.sweepRolledPartitionsLocked(retentionDays)
	} else {
		removed, err = s.runInputExpiryLocked(retentionDays, currentCutoff)
	}
	if err != nil {
		// What WAS removed is still gone: report it so the caller can move the
		// window past it, alongside the failure.
		return ExpiryNotice{RemovedPartitions: removed}, err
	}

	notice := ExpiryNotice{RemovedPartitions: removed}
	if retentionDays <= 0 {
		state, err := loadSavedInputLifecycle(s.dir(), s.sessionID)
		if err != nil {
			return notice, err
		}
		if len(state.Deletions) == 0 {
			return notice, nil // Disabled stops new decisions, not committed replay.
		}
	}
	w, found, err := s.loadWindowLocked()
	if err != nil {
		return notice, err
	}
	if !found {
		// No window yet: partitions may still have been removed, but there is no
		// mark to repair and no retained content to report.
		notice.Expired = removed > 0
		return notice, nil
	}

	repaired, first, emptied, changed, err := s.repairWindowLocked(w)
	if err != nil {
		return ExpiryNotice{}, err
	}
	if changed {
		if err := s.saveWindowLocked(repaired); err != nil {
			return ExpiryNotice{}, err
		}
	}
	notice.WindowEmptied = emptied
	notice.FirstRetained = first
	notice.Expired = removed > 0 || changed
	switch {
	case emptied:
		notice.Note = "retention expired all remaining model content; the window is now empty"
	case first != nil:
		notice.Note = fmt.Sprintf("retention expired older content; model window starts at %s", first.EntryID)
	case removed > 0:
		notice.Note = fmt.Sprintf("retention removed %d expired archive partition(s)", removed)
	}
	return notice, nil
}

// sweepRolledPartitionsLocked deletes every rolled "<key>.jsonl" partition whose
// mtime is strictly older than cutoff and returns the count. The current
// partition (current.jsonl) and the day mark are never touched.
func (s *ArchiveDayStore) sweepRolledPartitionsLocked(retentionDays int) (int, error) {
	return s.runInputExpiryLocked(retentionDays, nil)
}

// removePartitionFn deletes one expired rolled partition. A var so a test can
// make one removal fail.
var removePartitionFn = os.Remove

// repairWindowLocked advances a window past content that no longer resolves
// (its partition was swept) to the first retained COMPLETE group, and reports
// the new first retained slot. A model order whose leading slots are
// unresolvable OR orphaned tool results (role "tool" with no surviving producing
// assistant call ahead of them) is trimmed until a complete-group boundary is
// reached. When nothing complete remains the window is emptied while keeping its
// identity (same session ID).
//
// changed is true when the returned window differs from the input, so the
// caller writes only on an actual repair (idempotent, no rewrite-on-noop).
func (s *ArchiveDayStore) repairWindowLocked(w AddressedWindow) (AddressedWindow, *ArchiveAddress, bool, bool, error) {
	if len(w.Model) == 0 {
		return w, nil, false, false, nil
	}
	start := 0
	for start < len(w.Model) {
		rec, err := s.readAtLocked(w.Model[start])
		if err != nil {
			if partitionGone(err) {
				// The slot's partition was swept: expired.
				start++
				continue
			}
			// An I/O error or an unreadable record is not expiry. Advancing here
			// would shorten the persisted window and permanently drop context
			// over a transient failure, so it surfaces and nothing is saved.
			return w, nil, false, false, fmt.Errorf("archive: repair window: read slot %d: %w", start, err)
		}
		if slotIsOrphanToolResult(rec) {
			// A tool result cannot start a complete group.
			start++
			continue
		}
		break
	}

	if start == 0 {
		return w, nil, false, false, nil // nothing expired; leave the window as-is
	}

	if start >= len(w.Model) {
		// No complete content remains: an empty same-ID window.
		out := w
		out.Model = nil
		out.Source = nil
		out.HasStart = false
		out.Start = ArchiveAddress{}
		out.HasEnd = false
		out.End = ArchiveAddress{}
		out.Anchor = nil
		out.Excluded = nil
		out.Limits = nil
		return out, nil, true, true, nil
	}

	out := w
	out.Model = append([]ArchiveAddress(nil), w.Model[start:]...)
	sources, err := sourcesForSlotsLocked(s, out.Model)
	if err != nil {
		return w, nil, false, false, err
	}
	out.Source = sources
	out.HasStart = true
	out.Start = out.Model[0]
	out.Anchor = nil // an anchor before an expired start can no longer resolve
	first := out.Model[0]
	return out, &first, false, true, nil
}

// readAtLocked is ReadAt without the mutex (the caller already holds s.mu).
func (s *ArchiveDayStore) readAtLocked(addr ArchiveAddress) (ArchiveRecord, error) {
	if addr.EntryID == "" || addr.PartitionKey == "" || addr.ByteOffset < 0 {
		return ArchiveRecord{}, fmt.Errorf("archive: incomplete address %+v", addr)
	}
	path, err := s.partitionPathForKeyLocked(addr.PartitionKey)
	if err != nil {
		return ArchiveRecord{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return ArchiveRecord{}, err
	}
	defer f.Close()
	if _, err = f.Seek(addr.ByteOffset, 0); err != nil {
		return ArchiveRecord{}, err
	}
	rec, err := readArchiveRecordBounded(f)
	if err != nil {
		return ArchiveRecord{}, err
	}
	if rec.ID != addr.EntryID {
		return ArchiveRecord{}, fmt.Errorf("archive: address resolves to record %q, not %q", rec.ID, addr.EntryID)
	}
	return rec, nil
}

func (s *ArchiveDayStore) loadWindowLocked() (AddressedWindow, bool, error) {
	data, err := os.ReadFile(filepath.Join(s.dir(), archiveWindowFile))
	if errors.Is(err, fs.ErrNotExist) {
		return AddressedWindow{}, false, nil
	}
	if err != nil {
		return AddressedWindow{}, false, fmt.Errorf("archive: read window: %w", err)
	}
	var w AddressedWindow
	if err := json.Unmarshal(data, &w); err != nil {
		return AddressedWindow{}, false, fmt.Errorf("archive: decode window: %w", err)
	}
	return w, true, nil
}

func (s *ArchiveDayStore) saveWindowLocked(w AddressedWindow) error {
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
		return fmt.Errorf("archive: create dir: %w", err)
	}
	data, err := json.Marshal(w)
	if err != nil {
		return fmt.Errorf("archive: encode window: %w", err)
	}
	if err := fileutil.WriteFileAtomic(filepath.Join(s.dir(), archiveWindowFile), data, 0o600); err != nil {
		return fmt.Errorf("archive: write window: %w", err)
	}
	return nil
}

// slotIsOrphanToolResult reports whether a slot is a role "tool" payload that
// cannot begin a complete group — i.e. it carries no preceding producing
// assistant call. At a group boundary, a bare tool result is an orphan.
func slotIsOrphanToolResult(rec ArchiveRecord) bool {
	return rec.ModelMessage != nil && rec.ModelMessage.Role == "tool"
}

// sourcesForSlotsLocked resolves the source address of each model slot (a
// payload slot is its own source; a model_ref slot resolves to the payload it
// references), preserving model order.
func sourcesForSlotsLocked(s *ArchiveDayStore, slots []ArchiveAddress) ([]ArchiveAddress, error) {
	out := make([]ArchiveAddress, 0, len(slots))
	for _, slot := range slots {
		rec, err := s.readAtLocked(slot)
		if err != nil {
			return nil, fmt.Errorf("archive: resolve window source: %w", err)
		}
		if ref, ok := rec.ReferenceAddress(); ok {
			out = append(out, ref)
			continue
		}
		out = append(out, slot)
	}
	return out, nil
}

// expireSession is the ONE retention-expiry path for a session: it deletes the
// aged rolled partitions (SweepExpired), then moves the live backend cursor past
// every model slot whose record was expired, so the next window read resolves.
// UnifiedStore.RetentionSweep calls it for every session directory and no other
// code deletes a rolled day partition. The caller holds the session shard.
func (b *archiveBackend) expireSession(sessionID string, retentionDays int) (ExpiryNotice, error) {
	return b.expireSessionWithCurrent(sessionID, retentionDays, nil)
}

func (b *archiveBackend) expireSessionWithCurrent(sessionID string, retentionDays int, currentCutoff *time.Time) (ExpiryNotice, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	store, err := b.store(sessionID)
	if err != nil {
		return ExpiryNotice{}, err
	}
	// A failed removal must not stop the cursor following the partitions that
	// WERE removed (delta review R1 #6): advance whatever the sweep did, and
	// return both failures.
	notice, sweepErr := store.sweepExpiredWithCurrent(retentionDays, currentCutoff)
	moved, advErr := b.advancePastExpiredLocked(sessionID, store)
	if err := errors.Join(sweepErr, advErr); err != nil && !moved.moved {
		return notice, err
	}
	if moved.moved {
		// The window cursor moved past expired content: this is the FR-007 agent
		// expiry notice (SweepExpired only fills it from a persisted
		// AddressedWindow, which the live backend does not use).
		notice.Expired = true
		notice.WindowEmptied = moved.emptied
		notice.FirstRetained = moved.first
		notice.Note = moved.note
	}
	return notice, errors.Join(sweepErr, advErr)
}

// windowExpiry is what advancePastExpiredLocked did to the live cursor.
type windowExpiry struct {
	moved, emptied bool
	first          *ArchiveAddress // first retained slot; nil when emptied
	note           string
}

// expiryNoteText is the FR-007 agent expiry notice for a cursor that moved to
// newSkip of n slots.
func expiryNoteText(newSkip, n int) string {
	if newSkip >= n {
		return "Retention expired all remaining model content for this conversation; the context window is now empty."
	}
	return fmt.Sprintf("Retention expired older conversation content; the context window now starts at the first retained complete group (archive line %d).", newSkip)
}

// advancePastExpiredLocked moves meta.Skip to the first retained COMPLETE model
// slot after the last live slot whose record is gone, and drops an anchor whose
// record is gone. A slot is gone only when its partition is provably absent; any
// other failure returns an error and changes nothing. b.mu must be held.
func (b *archiveBackend) advancePastExpiredLocked(key string, store *ArchiveDayStore) (windowExpiry, error) {
	state, err := loadSavedInputLifecycle(store.dir(), store.sessionID)
	if err != nil {
		return windowExpiry{}, err
	}
	if len(state.Deletions) == 0 {
		return windowExpiry{}, nil
	}
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		return windowExpiry{}, err
	}
	store.mu.Lock()
	after, out, err := store.projectRetentionWindowLocked(meta, state)
	store.mu.Unlock()
	if err != nil {
		return windowExpiry{}, err
	}
	if meta.Skip == after.Skip && meta.Count == after.Count && meta.ClearFloor == after.ClearFloor && intPtrEqual(meta.AnchorLine, after.AnchorLine) {
		return out, nil
	}
	if err := b.saveMetaLocked(key, after); err != nil {
		return windowExpiry{}, err
	}
	store.observeWriteStep("expiry.window_projected")
	return out, nil
}

// rowResolvable reports whether the partitions a model slot reads from (the slot
// itself and, for a model_ref, its source) still exist. present caches the
// answer per partition key. Only a provably absent partition is "not resolvable";
// any other failure is returned.
func (s *ArchiveDayStore) rowResolvable(row ordinalRow, present map[string]bool) (bool, error) {
	for _, addr := range []ArchiveAddress{row.Slot, row.content()} {
		ok, cached := present[addr.PartitionKey]
		if !cached {
			s.mu.Lock()
			_, err := s.partitionPathForKeyLocked(addr.PartitionKey)
			s.mu.Unlock()
			switch {
			case err == nil:
				ok = true
			case errors.Is(err, errNoPartition):
				ok = false
			default:
				return false, err
			}
			present[addr.PartitionKey] = ok
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}
