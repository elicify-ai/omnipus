package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var ErrRevisionConflict = errors.New("skill revision conflict")

type PersistenceStatus string
type ActivationStatus string
type PublicationErrorStage string

const (
	PersistenceComplete PersistenceStatus = "complete"
	PersistenceNone     PersistenceStatus = "none"
	PersistencePartial  PersistenceStatus = "partial"

	ActivationActive       ActivationStatus = "active"
	ActivationFailed       ActivationStatus = "failed"
	ActivationNotAttempted ActivationStatus = "not_attempted"

	PublicationErrorStageRestorePrevious PublicationErrorStage = "restore_previous"
	// PublicationErrorStageSave names a storage failure before anything was
	// published (§7.6 class 1): nothing changed.
	PublicationErrorStageSave PublicationErrorStage = "save"
	// PublicationErrorStageRecordVersion names a change that is live but
	// whose version record could not be written (§7.6 class 2 / 2-create).
	PublicationErrorStageRecordVersion PublicationErrorStage = "record_version"
)

func (s PersistenceStatus) Valid() bool {
	return s == PersistenceComplete || s == PersistenceNone || s == PersistencePartial
}

func (s ActivationStatus) Valid() bool {
	return s == ActivationActive || s == ActivationFailed || s == ActivationNotAttempted
}

type PublishOutcome struct {
	Revision          string
	PersistenceStatus PersistenceStatus
	ActivationStatus  ActivationStatus
	ChangedFields     []string
	ErrorStage        PublicationErrorStage
	Message           string
	Warning           string
}

// renamePublishedSkill is the pipeline's staged→live publication rename
// (a test seam).
var renamePublishedSkill = os.Rename

var skillMutationLocks sync.Map

func mutationLock(root string) *sync.Mutex {
	key := filepath.Clean(root)
	value, _ := skillMutationLocks.LoadOrStore(key, &sync.Mutex{})
	mu, ok := value.(*sync.Mutex)
	if !ok {
		// Impossible by construction: only this function stores into the
		// private map, always &sync.Mutex{}. Fail loudly rather than return
		// a nil lock, which would silently drop mutation serialization.
		panic(fmt.Sprintf("skills: mutation lock entry for %q is %T, want *sync.Mutex", key, value))
	}
	return mu
}

// WithMutationLock serializes every checked mutation of a skill tree rooted at
// root. Callers must perform both revision comparison and publication inside fn.
func WithMutationLock(root string, fn func() error) error {
	mu := mutationLock(root)
	mu.Lock()
	defer mu.Unlock()
	return fn()
}

// SkillRevision returns a deterministic digest of the currently published
// package. Version snapshots are excluded because they are recovery metadata,
// not part of the active skill reviewed by the caller.
func (w *SkillWriter) SkillRevision(name string) (string, error) {
	dir, err := w.resolveSkillDir(name)
	if err != nil {
		return "", err
	}
	return revisionForDir(dir)
}

// revisionForDir is the published package's revision: §7.3's content
// revision (ContentRevision), so every caller — REST, the tools and the
// change pipeline — compares one revision definition. A folder without
// SKILL.md is not a published package here (ErrNotFound).
func revisionForDir(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNotFound
		}
		return "", err
	}
	return ContentRevision(dir)
}

func (w *SkillWriter) CreateSkillReviewed(name, content string) (path, revision string, err error) {
	err = WithMutationLock(w.root, func() error {
		var createErr error
		path, createErr = w.CreateSkill(name, content)
		if createErr != nil {
			return createErr
		}
		revision, createErr = w.SkillRevision(name)
		return createErr
	})
	return path, revision, err
}

func (w *SkillWriter) EditSkillReviewed(name, content string, allowCreateOverride bool, expectedRevision string) (path string, createdOverride bool, revision string, err error) {
	err = WithMutationLock(w.root, func() error {
		current, revisionErr := w.SkillRevision(name)
		if revisionErr != nil {
			return revisionErr
		}
		if expectedRevision == "" || expectedRevision != current {
			return fmt.Errorf("%w: reviewed %q, current %q", ErrRevisionConflict, expectedRevision, current)
		}
		path, createdOverride, revisionErr = w.EditSkill(name, content, allowCreateOverride)
		if revisionErr != nil {
			return revisionErr
		}
		revision, revisionErr = w.SkillRevision(name)
		return revisionErr
	})
	return path, createdOverride, revision, err
}

func (w *SkillWriter) CreateOverrideReviewed(name, content, sourceSkillFile, expectedRevision string) (path, revision string, err error) {
	sourceWriter := NewSkillWriter(filepath.Dir(filepath.Dir(sourceSkillFile)))
	err = WithMutationLock(w.root, func() error {
		if _, localErr := w.SkillRevision(name); localErr == nil {
			return fmt.Errorf("%w: local override appeared after review", ErrRevisionConflict)
		} else if !errors.Is(localErr, ErrNotFound) {
			return localErr
		}
		current, sourceErr := sourceWriter.SkillRevision(name)
		if sourceErr != nil {
			return sourceErr
		}
		if expectedRevision == "" || expectedRevision != current {
			return fmt.Errorf("%w: reviewed %q, current %q", ErrRevisionConflict, expectedRevision, current)
		}
		var created bool
		path, created, sourceErr = w.EditSkill(name, content, true)
		if sourceErr != nil {
			return sourceErr
		}
		if !created {
			return fmt.Errorf("%w: expected a new override", ErrRevisionConflict)
		}
		revision, sourceErr = w.SkillRevision(name)
		return sourceErr
	})
	return path, revision, err
}

func (w *SkillWriter) RemoveSkillReviewed(name, expectedRevision string) error {
	return WithMutationLock(w.root, func() error {
		current, err := w.SkillRevision(name)
		if err != nil {
			return err
		}
		if expectedRevision == "" || expectedRevision != current {
			return fmt.Errorf("%w: reviewed %q, current %q", ErrRevisionConflict, expectedRevision, current)
		}
		return w.RemoveSkill(name)
	})
}
