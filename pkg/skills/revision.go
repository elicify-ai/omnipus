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

type PublishOutcome struct {
	Revision          string
	PersistenceStatus string
	ActivationStatus  string
	ChangedFields     []string
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
	return value.(*sync.Mutex)
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
		if os.IsNotExist(err) {
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
			target, err := os.Readlink(path)
			if err != nil {
				return err
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
	return
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
	return
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
	return
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
		if err := os.MkdirAll(root, 0o755); err != nil {
			return err
		}
		next, revisionErr := revisionForDir(stagedDir)
		if revisionErr != nil {
			return fmt.Errorf("verify staged skill: %w", revisionErr)
		}
		backup := target + ".replace-backup"
		if exists {
			if err := removeStaleBackup(backup); err != nil {
				return err
			}
			if err := renamePublishedSkill(target, backup); err != nil {
				return fmt.Errorf("preserve previous skill: %w", err)
			}
		}
		if err := renamePublishedSkill(stagedDir, target); err != nil {
			if exists {
				if restoreErr := renamePublishedSkill(backup, target); restoreErr != nil {
					return errors.New("publish failed and previous package restore failed")
				}
			}
			return fmt.Errorf("publish staged skill: %w", err)
		}
		if exists {
			if err := removePublishedBackup(backup); err != nil {
				outcome = PublishOutcome{Revision: next, PersistenceStatus: "complete", ActivationStatus: "active", ChangedFields: []string{"installed"}, Warning: "previous package cleanup is incomplete"}
				return nil
			}
		}
		outcome = PublishOutcome{Revision: next, PersistenceStatus: "complete", ActivationStatus: "active", ChangedFields: []string{"installed"}}
		return nil
	})
	return outcome, err
}
