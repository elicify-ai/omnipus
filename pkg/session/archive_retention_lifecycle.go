package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// prepareInputExpiryLocked commits exact-incarnation authority BEFORE unlink.
// The archive mutex is held; runtime also owns the session shard(s). currentCutoff
// is non-nil only for the unified sweep's existing ordinary-current-file policy.
func (s *ArchiveDayStore) prepareInputExpiryLocked(days int, currentCutoff *time.Time) (savedInputLifecycle, error) {
	state, err := loadSavedInputLifecycle(s.dir(), s.sessionID)
	if err != nil {
		return state, err
	}
	idx, err := loadSavedInputIndexFile(filepath.Join(s.dir(), savedInputIndexFile), s.sessionID)
	if err != nil {
		return state, err
	}
	if err := s.validatePendingExpirySourcesLocked(idx, state); err != nil {
		return state, err
	}
	if days <= 0 {
		return state, nil // Prior committed work is replayed by the caller.
	}
	entries, err := os.ReadDir(s.dir())
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	mark, err := s.readDayMarkLocked()
	if err != nil {
		return state, err
	}
	cutoff := s.now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	candidates := make(map[string]time.Time)
	for _, name := range rolledPartitionNames(entries) {
		candidates[name] = cutoff
	}
	if currentCutoff != nil && mark != "" {
		candidates[archiveCurrentFile] = *currentCutoff
	}
	changed := false
	for _, entry := range entries {
		limit, candidate := candidates[entry.Name()]
		if !candidate || entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return state, err
		}
		if !info.ModTime().Before(limit) {
			continue
		}
		key := strings.TrimSuffix(entry.Name(), ".jsonl")
		if entry.Name() == archiveCurrentFile {
			key = mark
		}
		intent, err := s.inputDeletionIntentLocked(entry.Name(), key, days, limit)
		if err != nil {
			return state, err
		}
		if _, exists := state.Deletions[intent.ID]; exists {
			continue
		}
		state.Deletions[intent.ID] = intent
		changed = true
	}
	if !changed {
		return state, nil
	}
	// A valid server consumed marker is positive evidence. Preserve that fact
	// before its physical partition expires, including poll-before-save.
	if err := s.promotePolledInputsLocked(&state, len(idx) > 0); err != nil {
		return state, err
	}
	if err := s.captureRetiredNoticeSourcesLocked(&state); err != nil {
		return state, err
	}
	for id, entry := range idx {
		if _, terminal := state.ExpiredReports[id]; terminal || state.inputWasConsumed(entry.Address) || state.isNoticeInput(id) {
			continue
		}
		deletion, selected := state.deletionForAddress(entry.Address)
		if !selected {
			continue
		}
		receiptID := savedInputReceiptID(s.sessionID, entry.Address)
		noticeID := "saved-input-retention-expiry:" + receiptID
		state.ExpiredReports[id] = savedInputExpiration{ReceiptID: receiptID, InputID: id, Source: entry.Address,
			SavedSeq: entry.Seq, DeletionID: deletion.ID, ExpiredAt: deletion.CommittedAt, NoticeID: noticeID}
		state.Notices[receiptID] = savedInputNotice{ReceiptID: receiptID, OutcomeID: "retention-expiry:" + receiptID,
			CopyVersion: 1, CreatedAt: deletion.CommittedAt}
	}
	s.observeWriteStep("expiry.intent_prepared")
	if err := saveSavedInputLifecycle(s.dir(), state); err != nil {
		return state, err
	}
	s.observeWriteStep("expiry.intent_committed")
	return state, nil
}

func (s *ArchiveDayStore) validatePendingExpirySourcesLocked(idx savedInputIndex, state savedInputLifecycle) error {
	for id, entry := range idx {
		if expired, ok := state.ExpiredReports[id]; ok {
			if expired.Source != entry.Address {
				return fmt.Errorf("saved input %q conflicts with its expiry receipt", id)
			}
			continue
		}
		if state.inputWasConsumed(entry.Address) || state.retiredNoticeSource(entry.Address) {
			continue
		}
		_, rec, err := s.readAtRawLocked(entry.Address)
		if err == nil {
			err = rec.Validate()
		}
		if err != nil {
			return fmt.Errorf("retention: pending input %q has no authorized expiry: %w", id, err)
		}
		if rec.ID != id || rec.ViewMembership != ViewMembershipChat || rec.ModelMessage == nil {
			return fmt.Errorf("retention: pending input %q is not its saved payload", id)
		}
	}
	return nil
}

func (s *ArchiveDayStore) inputDeletionIntentLocked(name, key string, days int, cutoff time.Time) (savedInputDeletion, error) {
	size, mtime, digest, err := fingerprintSavedInputFile(filepath.Join(s.dir(), name))
	if err != nil {
		return savedInputDeletion{}, err
	}
	intent := savedInputDeletion{PartitionKey: key, FileName: name, SizeBytes: size, MtimeUnixNano: mtime,
		SHA256: digest, RetentionDays: days, CutoffUTC: cutoff.UTC(), CommittedAt: s.now().UTC(), RetiredModelRows: []ordinalRow{}}
	n, err := s.ordinalCountLocked()
	if err != nil {
		return intent, err
	}
	for from := 0; from < n; from += scanChunk {
		rows, err := s.readOrdinalRowsRepairedLocked(from, min(n, from+scanChunk))
		if err != nil {
			return intent, err
		}
		for _, row := range rows {
			if row.Slot.PartitionKey == key || row.content().PartitionKey == key {
				intent.RetiredModelRows = append(intent.RetiredModelRows, row)
			}
		}
	}
	identity, err := json.Marshal([]any{s.sessionID, key, name, size, mtime, digest})
	if err != nil {
		return intent, err
	}
	sum := sha256.Sum256(identity)
	intent.ID = hex.EncodeToString(sum[:])
	return intent, nil
}

func fingerprintSavedInputFile(path string) (size, mtime int64, digest string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, 0, "", err
	}
	if !info.Mode().IsRegular() {
		return 0, 0, "", fmt.Errorf("retention target %q is not a regular file", path)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return 0, 0, "", err
	}
	return info.Size(), info.ModTime().UnixNano(), hex.EncodeToString(h.Sum(nil)), nil
}

func (s *ArchiveDayStore) finishInputDeletionsLocked(state *savedInputLifecycle) (int, error) {
	ids := make([]string, 0, len(state.Deletions))
	for id := range state.Deletions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	removed := 0
	for _, id := range ids {
		intent := state.Deletions[id]
		path := filepath.Join(s.dir(), intent.FileName)
		size, mtime, digest, err := fingerprintSavedInputFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			if !intent.Unlinked {
				// Re-sync the committed authority even after an earlier publication
				// returned a durability error with new bytes already visible.
				if err := saveSavedInputLifecycle(s.dir(), *state); err != nil {
					return removed, err
				}
				intent.Unlinked = true
				state.Deletions[id] = intent
				if err := saveSavedInputLifecycle(s.dir(), *state); err != nil {
					return removed, err
				}
			}
			continue
		}
		if err != nil {
			return removed, err
		}
		matches := size == intent.SizeBytes && mtime == intent.MtimeUnixNano && digest == intent.SHA256
		if !matches {
			if intent.Unlinked {
				continue // A completed old incarnation never owns a replacement.
			}
			return removed, fmt.Errorf("retention: file incarnation changed for %s; replacement preserved", intent.FileName)
		}
		if err := saveSavedInputLifecycle(s.dir(), *state); err != nil {
			return removed, err
		}
		if err := removePartitionFn(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return removed, fmt.Errorf("retention: remove committed %s: %w", intent.FileName, err)
		}
		removed++
		s.observeWriteStep("expiry.partition_unlinked")
		intent.Unlinked = true
		state.Deletions[id] = intent
		if err := saveSavedInputLifecycle(s.dir(), *state); err != nil {
			return removed, err
		}
		s.observeWriteStep("expiry.unlink_checkpointed")
	}
	return removed, nil
}

func (s *ArchiveDayStore) cleanupTerminalInputIndexLocked(state savedInputLifecycle) error {
	path := filepath.Join(s.dir(), savedInputIndexFile)
	idx, err := loadSavedInputIndexFile(path, s.sessionID)
	if err != nil {
		return err
	}
	changed := false
	for id, entry := range idx {
		if expired, ok := state.ExpiredReports[id]; ok {
			if expired.Source != entry.Address {
				return fmt.Errorf("retention: input %q conflicts with committed expiry", id)
			}
			delete(idx, id)
			changed = true
		} else if state.inputWasConsumed(entry.Address) || state.retiredNoticeSource(entry.Address) {
			delete(idx, id)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := saveSavedInputIndexFile(path, s.sessionID, idx); err != nil {
		return err
	}
	s.observeWriteStep("expiry.index_cleaned")
	// This same-directory durable checkpoint also orders last-entry unlink.
	return saveSavedInputLifecycle(s.dir(), state)
}
