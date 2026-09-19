package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

var (
	removeStaleBackup     = os.RemoveAll
	removePublishedBackup = os.RemoveAll
	renamePublishedSkill  = os.Rename
)

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

func revisionForDir(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNotFound
		}
		return "", err
	}
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == versionsDir || strings.HasPrefix(rel, versionsDir+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "." {
			return nil
		}
		if _, err = io.WriteString(h, filepath.ToSlash(rel)+"\x00"); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(h, "%o\x00", info.Mode().Type()); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, readlinkErr := os.Readlink(path)
			if readlinkErr != nil {
				return readlinkErr
			}
			_, err = io.WriteString(h, target)
			return err
		}
		if entry.IsDir() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(h, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return "", fmt.Errorf("compute skill revision: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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

// PublishStagedSkill atomically publishes a fully validated package directory.
// An empty expectedRevision is a create-only request; replacement requires the
// exact current revision. The old directory is restored if final publication
// fails after it has been moved aside.
func PublishStagedSkill(root, name, stagedDir, expectedRevision string) (PublishOutcome, error) {
	w := NewSkillWriter(root)
	target, err := w.resolveSkillDir(name)
	if err != nil {
		return PublishOutcome{}, err
	}
	var outcome PublishOutcome
	err = WithMutationLock(root, func() error {
		current, currentErr := w.SkillRevision(name)
		exists := currentErr == nil
		if currentErr != nil && !errors.Is(currentErr, ErrNotFound) {
			return currentErr
		}
		if !exists && expectedRevision != "" {
			return fmt.Errorf("%w: reviewed %q, current resource is absent", ErrRevisionConflict, expectedRevision)
		}
		if exists && (expectedRevision == "" || expectedRevision != current) {
			return fmt.Errorf("%w: reviewed %q, current %q", ErrRevisionConflict, expectedRevision, current)
		}
		if mkErr := os.MkdirAll(root, 0o755); mkErr != nil {
			return mkErr
		}
		next, revisionErr := revisionForDir(stagedDir)
		if revisionErr != nil {
			return fmt.Errorf("verify staged skill: %w", revisionErr)
		}
		backup := target + ".replace-backup"
		if exists {
			if rmErr := removeStaleBackup(backup); rmErr != nil {
				return rmErr
			}
			if renameErr := renamePublishedSkill(target, backup); renameErr != nil {
				return fmt.Errorf("preserve previous skill: %w", renameErr)
			}
		}
		if pubErr := renamePublishedSkill(stagedDir, target); pubErr != nil {
			if exists {
				if restoreErr := renamePublishedSkill(backup, target); restoreErr != nil {
					outcome = PublishOutcome{
						PersistenceStatus: PersistencePartial,
						ActivationStatus:  ActivationNotAttempted,
						ChangedFields:     []string{"installed"},
						ErrorStage:        PublicationErrorStageRestorePrevious,
						Message:           "replacement publication failed and the previous package could not be restored; no live package is available",
					}
					return errors.Join(
						fmt.Errorf("publish staged skill: %w", pubErr),
						fmt.Errorf("restore previous skill: %w", restoreErr),
					)
				}
			}
			return fmt.Errorf("publish staged skill: %w", pubErr)
		}
		warning := ""
		if exists {
			if cleanupErr := removePublishedBackup(backup); cleanupErr != nil {
				warning = fmt.Sprintf("previous package cleanup is incomplete: %v", cleanupErr)
			}
		}
		outcome = PublishOutcome{Revision: next, PersistenceStatus: PersistenceComplete, ActivationStatus: ActivationActive, ChangedFields: []string{"installed"}, Warning: warning}
		return nil
	})
	return outcome, err
}
