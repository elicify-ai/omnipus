// Omnipus — the only destructive operations the .base view pipeline may perform.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// VerifiedManagedView reads a member only when BOTH the pipeline's record and
// the current file's marker/name agree. It does not enroll discovered files.
func (m *ViewMembership) VerifiedManagedView(base, name string) (*records.SavedView, []byte, error) {
	rel, ok := m.Bases[base][name]
	if !ok {
		return nil, nil, fmt.Errorf("knowledge: view %q is not a pipeline-owned member of %q", name, base)
	}
	return m.readManagedView(base, name, rel)
}

// RewriteManagedView locks the recorded CURRENT path and verifies its file
// inside that lock before the pipeline may replace its bytes.
func (m *ViewMembership) RewriteManagedView(base, name string, data []byte) (bool, error) {
	rel, ok := m.Bases[base][name]
	if !ok {
		return false, fmt.Errorf("knowledge: view %q is not a pipeline-owned member of %q", name, base)
	}
	if err := validateGeneratedView(base, name, rel, data); err != nil {
		return false, err
	}
	var written bool
	err := m.withManagedViewLock(rel, func() error {
		_, current, readErr := m.readManagedView(base, name, rel)
		if readErr != nil {
			return readErr
		}
		if bytes.Equal(current, data) {
			return nil
		}
		if writeErr := m.writeManagedViewBytes(rel, data); writeErr != nil {
			return writeErr
		}
		written = true
		return nil
	})
	return written, err
}

// DeleteManagedView requires a verified record identity, then persists
// revocation before unlinking. A failed unlink leaves an unmanaged file and a
// visible error; a failed record save cannot leave an old path authorized to
// delete a replacement planted after the original was removed.
func (m *ViewMembership) DeleteManagedView(base, name string) (bool, error) {
	rel, ok := m.Bases[base][name]
	if !ok {
		return false, nil
	}
	var deleted bool
	err := m.withManagedViewLock(rel, func() error {
		_, _, readErr := m.readManagedView(base, name, rel)
		if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
			return readErr
		}
		var parent *os.Root
		var leaf string
		if readErr == nil {
			var parentErr error
			parent, leaf, parentErr = m.managedViewParent(rel)
			if parentErr != nil {
				return parentErr
			}
			defer parent.Close()
		}
		delete(m.Bases[base], name)
		if len(m.Bases[base]) == 0 {
			delete(m.Bases, base)
		}
		if saveErr := SaveViewMembership(m); saveErr != nil {
			if m.Bases[base] == nil {
				m.Bases[base] = make(map[string]string)
			}
			m.Bases[base][name] = rel
			return saveErr
		}
		if parent == nil {
			return nil
		}
		if removeErr := parent.Remove(leaf); removeErr != nil {
			if errors.Is(removeErr, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("knowledge: view membership revoked but delete incomplete for %q: %w", rel, removeErr)
		}
		deleted = true
		return nil
	})
	return deleted, err
}

// CreateManagedView creates only at a free path (O_EXCL), then enrolls that
// exact path. fs.ErrExist signals the caller to try a suffixed filename.
func (m *ViewMembership) CreateManagedView(base, name, rel string, data []byte) error {
	if !validMembershipPath(base, ".base") || !validMembershipPath(rel, ".view") {
		return fmt.Errorf("knowledge: invalid managed view path %q for %q", rel, base)
	}
	if err := validateGeneratedView(base, name, rel, data); err != nil {
		return err
	}
	return m.withManagedViewLock(rel, func() error {
		if _, owns := m.Bases[base][name]; owns {
			return fmt.Errorf("knowledge: view %q is already managed by %q", name, base)
		}
		for otherBase, names := range m.Bases {
			for _, memberRel := range names {
				if memberRel == rel {
					return fmt.Errorf("knowledge: path %q already belongs to %q: %w", rel, otherBase, fs.ErrExist)
				}
			}
		}
		parent, leaf, parentErr := m.managedViewParent(rel)
		if parentErr != nil {
			return parentErr
		}
		defer parent.Close()
		// O_EXCL refuses a concurrent file, directory, or symlink. All later
		// operations remain anchored to this verified parent directory.
		file, openErr := parent.OpenFile(leaf, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if openErr != nil {
			return openErr
		}
		created, statErr := file.Stat()
		if statErr != nil {
			return errors.Join(statErr, file.Close())
		}
		_, writeErr := file.Write(data)
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return errors.Join(writeErr, closeErr, removeCreatedView(parent, leaf, created))
		}
		if m.Bases[base] == nil {
			m.Bases[base] = make(map[string]string)
		}
		m.Bases[base][name] = rel
		if saveErr := SaveViewMembership(m); saveErr != nil {
			delete(m.Bases[base], name)
			if len(m.Bases[base]) == 0 {
				delete(m.Bases, base)
			}
			return errors.Join(saveErr, removeCreatedView(parent, leaf, created))
		}
		return nil
	})
}

func validateGeneratedView(base, name, rel string, data []byte) error {
	if len(data) > 256*1024 {
		return fmt.Errorf("knowledge: generated view exceeds 256 KiB: %w", io.ErrShortBuffer)
	}
	view, rejection := records.ParseView(rel, data)
	if rejection != nil {
		return fmt.Errorf("knowledge: generated view %q: %s", rel, rejection.Reason)
	}
	if view.Def.Name != name || view.Def.DerivedFrom == nil || *view.Def.DerivedFrom != base {
		return fmt.Errorf("knowledge: generated view %q does not match its pipeline-owned membership", rel)
	}
	return nil
}
