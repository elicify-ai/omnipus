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

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// Staging lifecycle states (Staging lifecycle table; FR-022, FR-039).
type StagingState string

const (
	StagingReceiving    StagingState = "receiving"
	StagingReady        StagingState = "ready"
	StagingApplying     StagingState = "applying"
	StagingDone         StagingState = "done"
	StagingNeedsCleanup StagingState = "needs-cleanup"

	// StagingCancelled (B1 cancel lifecycle; spec "Cancel lifecycle"): a ready
	// staging whose folder could not be removed. It can never be applied, does
	// not count as a slot, and its folder removal is attempted again by the
	// next upload's Sweep or a repeated cancel — never on a timer or at boot.
	StagingCancelled StagingState = "cancelled"
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

	// RemoveDir is nil in production. A test sets it to make folder removal
	// fail once (the B1 cancel spec's "test seam makes the removal fail"):
	// when set, it replaces os.RemoveAll for Cancel's folder removal only.
	RemoveDir func(dir string) error

	// AfterBeginApply is nil in production. A test sets it to pause an apply
	// right after BeginApply moved the staging to applying (the B1
	// cancel/apply interleaving checkpoint). Called with the registry lock
	// RELEASED, so the test's concurrent cancel can take the lock; the apply
	// resumes when the function returns (a channel handshake, never a sleep).
	AfterBeginApply func(id string)
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
	dir := filepath.Join(r.base, id)
	if err := mkdirAllSynced(dir); err != nil {
		if rmErr := os.Remove(dir); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove staging folder: %w", rmErr))
		}
		return "", fmt.Errorf("omnpkg: create staging folder: %w", err)
	}
	r.entries[id] = &stagingEntry{state: StagingReceiving}
	return id, nil
}

// mkdirAllSynced creates dir and any missing ancestors, then syncs the parent
// of every folder it created, so each new folder's entry is durable. The
// journal written inside the staging folder later syncs only that folder, so
// without this a power cut can lose the folder, journal included.
func mkdirAllSynced(dir string) error {
	var created []string
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(d); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		created = append(created, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, d := range created {
		if err := fileutil.SyncDir(filepath.Dir(d)); err != nil {
			return err
		}
	}
	return nil
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
	e, ok := r.entries[id]
	if !ok || e.state != StagingReady || r.expiredLocked(e) {
		r.mu.Unlock()
		return newErr(CodeStagingNotFound)
	}
	for _, other := range r.entries {
		if other.state == StagingApplying {
			r.mu.Unlock()
			return newErr(CodeImportInProgress)
		}
	}
	e.state = StagingApplying
	hook := r.AfterBeginApply
	r.mu.Unlock()
	// Test seam (nil in production): pauses the apply at the B1 interleaving
	// checkpoint — the state change is visible, the lock is released, so a
	// concurrent cancel observes "applying" (spec "Cancel and apply at the
	// same moment"). Behaviour with the hook nil is unchanged.
	if hook != nil {
		hook(id)
	}
	return nil
}

// errCancelNotStubbed is what the RED cancel stub returns; GREEN removes it
// with the stub body.
var errCancelNotStubbed = errors.New("omnpkg: staging cancel not implemented (RED stub, issue #1967 slice B1)")

// Cancel is the B1 ready-only cancel (spec "Cancel lifecycle"): one registry
// operation under the single lock; a ready staging's folder is removed and the
// entry dropped (204); on removal failure the entry moves to cancelled and the
// caller reports staging_cleanup_failed; receiving, applying and
// needs-cleanup answer staging_busy; done and unknown IDs answer
// staging_not_found. It is not Release: a cancelled staging keeps its entry
// (never applicable, not a slot) until a removal succeeds.
//
// RED STUB: returns errCancelNotStubbed for every input.
func (r *StagingRegistry) Cancel(id string) error {
	_ = id
	return errCancelNotStubbed
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
