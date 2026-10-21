package session

import (
	"errors"
	"path/filepath"
	"time"
)

// runInputExpiryLocked is session-local: durable intent, physical retirement,
// then recoverable pending-index projection. It never infers a new expiry from
// a missing source. Both callers and writers use the same archive exclusion.
func (s *ArchiveDayStore) runInputExpiryLocked(days int, currentCutoff *time.Time) (int, error) {
	state, err := loadSavedInputLifecycle(s.dir(), s.sessionID)
	if err != nil {
		return 0, err
	}
	if days <= 0 && len(state.Deletions) == 0 {
		return 0, nil
	}
	idx, err := loadSavedInputIndexFile(filepath.Join(s.dir(), savedInputIndexFile), s.sessionID)
	if err != nil {
		return 0, err
	}
	if err := s.validatePendingExpirySourcesLocked(idx, state); err != nil {
		return 0, err
	}
	removed, err := s.finishInputDeletionsLocked(&state)
	if err != nil {
		return removed, err
	}
	if err := s.cleanupTerminalInputIndexLocked(state); err != nil {
		return removed, err
	}
	if days <= 0 {
		return removed, nil
	}
	meta, err := loadArchiveBackendMeta(s.dir())
	if err != nil {
		return removed, err
	}
	if _, _, err := s.projectRetentionWindowLocked(meta, state); err != nil {
		return removed, err
	}
	state, err = s.prepareInputExpiryLocked(days, currentCutoff)
	if err != nil {
		return removed, err
	}
	n, unlinkErr := s.finishInputDeletionsLocked(&state)
	removed += n
	cleanupErr := s.cleanupTerminalInputIndexLocked(state)
	return removed, errors.Join(unlinkErr, cleanupErr)
}

// resumeInputDeletionsLocked is also the append-target reuse barrier. It does
// not create expiry decisions or publish notices while an archive lock is held.
func (s *ArchiveDayStore) resumeInputDeletionsLocked() error {
	_, err := s.runInputExpiryLocked(0, nil)
	return err
}
