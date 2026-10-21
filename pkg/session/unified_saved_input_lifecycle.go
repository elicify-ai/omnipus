package session

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RecoverSavedInputLifecycle finishes already-committed expiry, even when new
// retention is disabled. It does not create a new age-based loss decision.
func (us *UnifiedStore) RecoverSavedInputLifecycle(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	return us.recoverSavedInputLifecycleLocked(sessionID)
}

func (us *UnifiedStore) recoverSavedInputLifecycleLocked(sessionID string) error {
	state, err := loadSavedInputLifecycle(filepath.Join(us.baseDir, sessionID), sessionID)
	if err != nil {
		return err
	}
	if len(state.Deletions) == 0 && len(state.Notices) == 0 {
		return nil
	}
	if _, err := us.readMetaLocked(sessionID); err != nil {
		return fmt.Errorf("retention recovery: recipient metadata: %w", err)
	}
	archive, err := us.archiveStore(sessionID)
	if err != nil {
		return err
	}
	replayErr := func() error {
		us.backend.mu.Lock()
		defer us.backend.mu.Unlock()
		err := func() error {
			archive.mu.Lock()
			defer archive.mu.Unlock()
			_, err := archive.runInputExpiryLocked(0, nil)
			return err
		}()
		if err != nil {
			return err
		}
		_, err = us.backend.advancePastExpiredLocked(sessionID, archive)
		return err
	}()
	if replayErr != nil {
		return replayErr
	}
	return us.prepareRetentionNoticeInputsLocked(sessionID)
}

func (us *UnifiedStore) recoverAllSavedInputLifecycles(ctx context.Context) error {
	entries, err := os.ReadDir(us.baseDir)
	if err != nil {
		return err
	}
	var failures error
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		us.cacheMu.RLock()
		_, readableSession := us.metaCache[entry.Name()]
		us.cacheMu.RUnlock()
		if !readableSession {
			continue // Preserve the existing unreadable/orphan identity quarantine.
		}
		if _, err := os.Stat(filepath.Join(us.baseDir, entry.Name(), savedInputLifecycleFile)); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		if err := us.RecoverSavedInputLifecycle(ctx, entry.Name()); err != nil {
			failures = errors.Join(failures, fmt.Errorf("session %s: retention recovery: %w", entry.Name(), err))
		}
	}
	return failures
}

// RecordSavedInputPollConsumed is the durable final-read claim. It precedes the
// compatibility chat marker/inbox acknowledgement; its body-free fact therefore
// survives marker expiry and the poll-before-save ordering.
func (us *UnifiedStore) RecordSavedInputPollConsumed(sessionID, inputID string) (bool, error) {
	if err := validateSessionID(sessionID); err != nil {
		return false, err
	}
	if inputID == "" {
		return false, fmt.Errorf("saved input poll requires an input id")
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	if _, err := us.readMetaLocked(sessionID); err != nil {
		return false, err
	}
	idx, err := loadSavedInputIndexFile(us.savedInputIndexPath(sessionID), sessionID)
	if err != nil {
		return false, err
	}
	archive, err := us.archiveStore(sessionID)
	if err != nil {
		return false, err
	}
	archive.mu.Lock()
	defer archive.mu.Unlock()
	state, err := loadSavedInputLifecycle(archive.dir(), sessionID)
	if err != nil {
		return false, err
	}
	if err := state.expiry(inputID); err != nil {
		return false, err
	}
	if _, exists := state.PollConsumed[inputID]; exists {
		return true, nil
	}
	effective, err := state.pendingProjection(idx)
	if err != nil {
		return false, err
	}
	if err := archive.checkMissingCurrentPublicationLocked(effective); err != nil {
		return false, err
	}
	if _, placed, err := archive.placedInputByIDLocked(inputID); err != nil || placed {
		return placed, err
	}
	poll := savedInputPoll{InputID: inputID, ConsumedAt: time.Now().UTC(), MarkerID: "consumed-" + strings.TrimPrefix(inputID, "saved-input-")}
	if entry, ok := idx[inputID]; ok {
		_, rec, err := archive.readAtRawLocked(entry.Address)
		if err != nil {
			return false, err
		}
		if rec.ViewMembership != ViewMembershipChat || rec.ModelMessage == nil {
			return false, fmt.Errorf("saved input %q is not its pending payload", inputID)
		}
		poll.Source = &entry.Address
	}
	state.PollConsumed[inputID] = poll
	if err := saveSavedInputLifecycle(archive.dir(), state); err != nil {
		return false, err
	}
	return false, nil
}

// SavedInputConsumed is a terminal-disposition query for engine/tool consumers.
// Expiry returns its typed error, never a falsely "delivered" boolean.
func (us *UnifiedStore) SavedInputConsumed(sessionID, inputID string) (bool, error) {
	if err := validateSessionID(sessionID); err != nil {
		return false, err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	if _, err := us.readMetaLocked(sessionID); err != nil {
		return false, err
	}
	idx, err := loadSavedInputIndexFile(us.savedInputIndexPath(sessionID), sessionID)
	if err != nil {
		return false, err
	}
	state, err := loadSavedInputLifecycle(filepath.Join(us.baseDir, sessionID), sessionID)
	if err != nil {
		return false, err
	}
	if err := state.expiry(inputID); err != nil {
		return false, err
	}
	if _, ok := state.PollConsumed[inputID]; ok {
		return true, nil
	}
	archive, err := us.archiveStore(sessionID)
	if err != nil {
		return false, err
	}
	archive.mu.Lock()
	defer archive.mu.Unlock()
	effective, err := state.pendingProjection(idx)
	if err != nil {
		return false, err
	}
	if err := archive.checkMissingCurrentPublicationLocked(effective); err != nil {
		return false, err
	}
	_, placed, err := archive.placedInputByIDLocked(inputID)
	return placed, err
}

func (s *ArchiveDayStore) placedInputByIDLocked(inputID string) (ArchiveAddress, bool, error) {
	n, err := s.ordinalCountLocked()
	if err != nil {
		return ArchiveAddress{}, false, err
	}
	var source ArchiveAddress
	for from := 0; from < n; from += scanChunk {
		rows, err := s.readOrdinalRowsRepairedLocked(from, min(n, from+scanChunk))
		if err != nil {
			return ArchiveAddress{}, false, err
		}
		for _, row := range rows {
			if row.Source == nil || row.Source.EntryID != inputID {
				continue
			}
			if source.EntryID != "" && source != *row.Source {
				return ArchiveAddress{}, false, fmt.Errorf("saved input %q has conflicting published sources", inputID)
			}
			source = *row.Source
		}
	}
	return source, source.EntryID != "", nil
}

func savedInputNoticeIDs(state savedInputLifecycle) []string {
	ids := make([]string, 0, len(state.Notices))
	for id := range state.Notices {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
