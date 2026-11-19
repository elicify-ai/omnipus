package omnpkg

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Staging lifecycle states (Staging lifecycle table; FR-022, FR-039).
type StagingState string

const (
	StagingReceiving    StagingState = "receiving"
	StagingReady        StagingState = "ready"
	StagingApplying     StagingState = "applying"
	StagingDone         StagingState = "done"
	StagingNeedsCleanup StagingState = "needs-cleanup"
)

// UnfinishedImport names a kept orphan staging for the unfinished_import_found
// warning (FR-044): agent ID and staging folder.
type UnfinishedImport struct {
	StagingID string
	AgentID   string
}

type stagingEntry struct {
	state   StagingState
	readyAt time.Time
}

// StagingRegistry is the in-memory staging registry guarded by one lock
// (Staging lifecycle). now is the clock (fake in tests); base is the staging
// root folder ($OMNIPUS_HOME/backups/.staging) for orphan handling.
type StagingRegistry struct {
	mu      sync.Mutex
	base    string
	now     func() time.Time
	entries map[string]*stagingEntry
	orphans map[string]UnfinishedImport // kept orphan folders with an unfinished journal
}

// NewStagingRegistry builds a registry over base with the given clock.
func NewStagingRegistry(base string, now func() time.Time) *StagingRegistry {
	return &StagingRegistry{
		base:    base,
		now:     now,
		entries: map[string]*stagingEntry{},
		orphans: map[string]UnfinishedImport{},
	}
}

// Dir returns the folder of a registered staging.
func (r *StagingRegistry) Dir(id string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[id]; !ok {
		return "", newErr(CodeStagingNotFound)
	}
	return filepath.Join(r.base, id), nil
}

// expiredLocked reports a ready staging at or past its 30-minute expiry.
func (r *StagingRegistry) expiredLocked(e *stagingEntry) bool {
	return e.state == StagingReady && !r.now().Before(e.readyAt.Add(StagingReadyTTL))
}

// slotsUsedLocked counts receiving, unexpired ready, applying, needs-cleanup
// and kept orphans.
func (r *StagingRegistry) slotsUsedLocked() int {
	used := len(r.orphans)
	for _, e := range r.entries {
		switch e.state {
		case StagingReceiving, StagingApplying, StagingNeedsCleanup:
			used++
		case StagingReady:
			if !r.expiredLocked(e) {
				used++
			}
		}
	}
	return used
}

// Reserve claims a slot (counting every live state plus kept orphans; at most
// MaxStagedImports) before decoding (FR-022) and creates the staging folder.
func (r *StagingRegistry) Reserve() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.slotsUsedLocked() >= MaxStagedImports {
		return "", newErr(CodeTooManyStagedImports, "limit", fmt.Sprint(MaxStagedImports))
	}
	u, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("omnpkg: generate staging ID: %w", err)
	}
	id := u.String()
	if err := os.MkdirAll(filepath.Join(r.base, id), 0o700); err != nil {
		return "", fmt.Errorf("omnpkg: create staging folder: %w", err)
	}
	r.entries[id] = &stagingEntry{state: StagingReceiving}
	return id, nil
}

// MarkReady moves a receiving staging to ready; the expiry clock starts here.
func (r *StagingRegistry) MarkReady(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return newErr(CodeStagingNotFound)
	}
	if e.state != StagingReceiving {
		return fmt.Errorf("omnpkg: staging %s is %s, not %s", id, e.state, StagingReceiving)
	}
	e.state = StagingReady
	e.readyAt = r.now()
	return nil
}

// State reports the staging's current state.
func (r *StagingRegistry) State(id string) (StagingState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return "", newErr(CodeStagingNotFound)
	}
	return e.state, nil
}

// BeginApply atomically moves ready → applying (404 staging_not_found for any
// other state or an expired staging; 409 import_in_progress when an apply is
// already running anywhere, FR-038).
func (r *StagingRegistry) BeginApply(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok || e.state != StagingReady || r.expiredLocked(e) {
		return newErr(CodeStagingNotFound)
	}
	for _, other := range r.entries {
		if other.state == StagingApplying {
			return newErr(CodeImportInProgress)
		}
	}
	e.state = StagingApplying
	return nil
}

// AbortApply returns an applying staging to ready after a refusal that wrote
// nothing, with the expiry clock unchanged (Staging lifecycle "End").
func (r *StagingRegistry) AbortApply(id string) error {
	return r.transition(id, StagingApplying, func(e *stagingEntry) { e.state = StagingReady })
}

// FinishApply completes an apply: done, folder deleted, ID consumed. The state
// is done even when the folder delete fails (the error is returned; the next
// sweep removes the folder through its finished journal).
func (r *StagingRegistry) FinishApply(id string) error {
	if err := r.transition(id, StagingApplying, func(e *stagingEntry) { e.state = StagingDone }); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(r.base, id)); err != nil {
		return fmt.Errorf("omnpkg: delete finished staging folder: %w", err)
	}
	return nil
}

// NeedsCleanup moves a live staging to needs-cleanup (incomplete rollback):
// kept, never expires, counts in the slots (FR-022).
func (r *StagingRegistry) NeedsCleanup(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok || e.state == StagingDone {
		return newErr(CodeStagingNotFound)
	}
	e.state = StagingNeedsCleanup
	return nil
}

// Release drops a failed upload's slot and folder. The slot is freed even when
// the folder delete fails (the error is returned; the next sweep removes the
// journal-less folder).
func (r *StagingRegistry) Release(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[id]; !ok {
		return newErr(CodeStagingNotFound)
	}
	delete(r.entries, id)
	if err := os.RemoveAll(filepath.Join(r.base, id)); err != nil {
		return fmt.Errorf("omnpkg: delete released staging folder: %w", err)
	}
	return nil
}

func (r *StagingRegistry) transition(id string, from StagingState, apply func(*stagingEntry)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return newErr(CodeStagingNotFound)
	}
	if e.state != from {
		return fmt.Errorf("omnpkg: staging %s is %s, not %s", id, e.state, from)
	}
	apply(e)
	return nil
}

// Sweep is the per-upload maintenance: drop needs-cleanup entries whose folder
// is gone, delete expired ready stagings and orphan folders whose journal is
// absent or terminal (done / rolled_back), keep unfinished-journal orphans
// (started / committed / rollback_incomplete — they hold a slot and are
// returned for the unfinished_import_found warning, FR-044). A folder that
// cannot be deleted stays and is retried by the next sweep.
func (r *StagingRegistry) Sweep() []UnfinishedImport {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, e := range r.entries {
		switch {
		case e.state == StagingNeedsCleanup && !r.folderExists(id):
			delete(r.entries, id)
		case r.expiredLocked(e):
			if os.RemoveAll(filepath.Join(r.base, id)) == nil {
				delete(r.entries, id)
			}
		}
	}
	r.orphans = r.scanOrphansLocked()
	kept := make([]UnfinishedImport, 0, len(r.orphans))
	for _, u := range r.orphans {
		kept = append(kept, u)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].StagingID < kept[j].StagingID })
	return kept
}

// scanOrphansLocked walks the staging root for folders the registry does not
// hold (left by a restart): finished or journal-less ones are deleted,
// unfinished ones are kept. A journal that exists but cannot be read is kept
// as unfinished (never delete what recovery may still need).
func (r *StagingRegistry) scanOrphansLocked() map[string]UnfinishedImport {
	kept := map[string]UnfinishedImport{}
	dirEntries, err := os.ReadDir(r.base)
	if err != nil {
		return kept
	}
	for _, de := range dirEntries {
		id := de.Name()
		if !de.IsDir() {
			continue
		}
		if e, live := r.entries[id]; live && e.state != StagingDone {
			continue
		}
		dir := filepath.Join(r.base, id)
		j, jerr := ReadJournal(dir)
		switch {
		case jerr == nil && !j.State.Terminal():
			kept[id] = UnfinishedImport{StagingID: id, AgentID: j.AgentID}
		case jerr == nil || errors.Is(jerr, fs.ErrNotExist):
			_ = os.RemoveAll(dir) // retried by the next sweep when it fails
		default:
			kept[id] = UnfinishedImport{StagingID: id}
		}
	}
	return kept
}

func (r *StagingRegistry) folderExists(id string) bool {
	info, err := os.Stat(filepath.Join(r.base, id))
	return err == nil && info.IsDir()
}
