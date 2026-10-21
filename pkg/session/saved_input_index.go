package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// savedInputIndexFile is the content-free directory of a session's saved wake
// inputs (PW-D16). The archive stays the only content authority: an entry
// holds the source address and the ordinal count at save time, never the
// body. A session that never received a saved input has no file, so the
// per-turn pending check costs one missing-file stat and no archive read.
const savedInputIndexFile = "saved_inputs.json"

// savedInputIndexEntry tracks one saved input until it is placed into model
// history or consumed by a poll. // not-wire-format: internal sidecar only.
type savedInputIndexEntry struct {
	// Address is the saved chat record; empty while the save is in flight.
	Address ArchiveAddress `json:"address"`
	// FromOrdinal is the session's model-slot count when the input was saved.
	// A placement of this source can only occupy a later ordinal, so the
	// once-only check reads rows from here, not the lifetime of the session.
	FromOrdinal int `json:"from_ordinal"`
	// Seq orders pending inputs by arrival; ids alone do not.
	Seq int64 `json:"seq"`
}

// savedInputIndex maps saved-input id -> entry. // not-wire-format
type savedInputIndex map[string]savedInputIndexEntry

func (us *UnifiedStore) savedInputIndexPath(sessionID string) string {
	return filepath.Join(us.baseDir, sessionID, savedInputIndexFile)
}

// loadSavedInputIndexLocked reads the sidecar. The caller holds the session
// lock. A missing file is an empty index; a corrupt one is an error, never a
// silently empty set (that would strand saved inputs).
func (us *UnifiedStore) loadSavedInputIndexLocked(sessionID string) (savedInputIndex, error) {
	idx, err := loadSavedInputIndexFile(us.savedInputIndexPath(sessionID), sessionID)
	if err != nil || len(idx) == 0 {
		return idx, err
	}
	state, err := loadSavedInputLifecycle(filepath.Join(us.baseDir, sessionID), sessionID)
	if err != nil {
		return nil, err
	}
	return state.pendingProjection(idx)
}

// loadSavedInputIndexFile is the shared strict RAW decoder. Retention must
// classify original ownership before applying any terminal receipt projection.
func loadSavedInputIndexFile(path, sessionID string) (savedInputIndex, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return savedInputIndex{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("saved input index %q: read: %w", sessionID, err)
	}
	idx := savedInputIndex{}
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("saved input index %q: parse: %w", sessionID, err)
	}
	if idx == nil {
		return nil, fmt.Errorf("saved input index %q: expected a JSON object, got null", sessionID)
	}
	for id, entry := range idx {
		if id == "" || entry.Address.EntryID != id || entry.Address.PartitionKey == "" ||
			entry.Address.ByteOffset < 0 || entry.FromOrdinal < 0 {
			return nil, fmt.Errorf("saved input index %q: invalid entry %q", sessionID, id)
		}
	}
	return idx, nil
}

// savedInputIndexWriteFault, when set, fails pending-index writes. Tests only:
// it reproduces a crash or disk failure between the archive append and the
// index write. Set it through SetSavedInputIndexWriteFaultForTest.
var savedInputIndexWriteFault atomic.Pointer[func(sessionID string) error]

// SetSavedInputIndexWriteFaultForTest installs (fn != nil) or clears (nil) the
// pending-index write fault. Never call this outside tests.
func SetSavedInputIndexWriteFaultForTest(fn func(sessionID string) error) {
	if fn == nil {
		savedInputIndexWriteFault.Store(nil)
		return
	}
	savedInputIndexWriteFault.Store(&fn)
}

func (us *UnifiedStore) saveSavedInputIndexLocked(sessionID string, idx savedInputIndex) error {
	return saveSavedInputIndexFile(us.savedInputIndexPath(sessionID), sessionID, idx)
}

func saveSavedInputIndexFile(path, sessionID string, idx savedInputIndex) error {
	if fault := savedInputIndexWriteFault.Load(); fault != nil {
		if err := (*fault)(sessionID); err != nil {
			return fmt.Errorf("saved input index %q: write: %w", sessionID, err)
		}
	}
	if len(idx) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("saved input index %q: clear: %w", sessionID, err)
		}
		return nil
	}
	data, err := json.Marshal(idx)
	if err != nil {
		return fmt.Errorf("saved input index %q: encode: %w", sessionID, err)
	}
	if err := fileutil.WriteFileAtomicSyncDir(path, data, 0o600); err != nil {
		return fmt.Errorf("saved input index %q: write: %w", sessionID, err)
	}
	return nil
}

// RetireSavedInput removes a saved input from the pending directory once it
// has been placed or consumed by another path (a poll's consumed marker). The
// archive record stays; only the content-free pending entry goes.
func (us *UnifiedStore) RetireSavedInput(sessionID, id string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	return us.retireSavedInputLocked(sessionID, id)
}

func (us *UnifiedStore) retireSavedInputLocked(sessionID, id string) error {
	idx, err := us.loadSavedInputIndexLocked(sessionID)
	if err != nil {
		return err
	}
	if _, ok := idx[id]; !ok {
		return nil
	}
	delete(idx, id)
	return us.saveSavedInputIndexLocked(sessionID, idx)
}

func (us *UnifiedStore) retireSavedInputByAddressLocked(sessionID string, addr ArchiveAddress) error {
	idx, err := us.loadSavedInputIndexLocked(sessionID)
	if err != nil {
		return err
	}
	for id, entry := range idx {
		if entry.Address == addr {
			delete(idx, id)
			return us.saveSavedInputIndexLocked(sessionID, idx)
		}
	}
	return nil
}

func sortedSavedInputIDs(idx savedInputIndex) []string {
	ids := make([]string, 0, len(idx))
	for id := range idx {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if idx[ids[i]].Seq != idx[ids[j]].Seq {
			return idx[ids[i]].Seq < idx[ids[j]].Seq
		}
		return ids[i] < ids[j]
	})
	return ids
}
