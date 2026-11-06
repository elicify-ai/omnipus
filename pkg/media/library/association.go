// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package library

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/google/uuid"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Group chat additions (#1449):
//
//   - retention owners (FR-076): a persistent, idempotent owner set per
//     entry. OrphanGC never removes an owned entry; only an explicit Delete
//     removes it, and deleting a group never releases its owners (D19).
//   - the Library-file association (D26, FR-086): the entry's library_path,
//     the workspace-relative path of its Library file under .library. One
//     explicit Library deletion removes both copies.
//   - explicit-deletion tombstones: a create-or-adopt publication under a
//     preallocated id (FR-078) never recreates an entry the person deleted.

// ErrDeletedByUser: the id was deleted explicitly; it is never recreated.
var ErrDeletedByUser = errors.New("media library: entry was deleted explicitly")

// ErrContentMismatch: an entry with the preallocated id exists with other
// bytes.
var ErrContentMismatch = errors.New("media library: entry exists with different content")

func (l *Library) loadEnvelopeLocked(persisted manifestFile) {
	l.owners = make(map[string]map[string]bool, len(persisted.RetentionOwners))
	for id, owners := range persisted.RetentionOwners {
		if _, ok := l.manifest[id]; !ok || len(owners) == 0 {
			continue
		}
		set := make(map[string]bool, len(owners))
		for _, o := range owners {
			set[o] = true
		}
		l.owners[id] = set
	}
	l.deleted = make(map[string]bool, len(persisted.DeletedIDs))
	for _, id := range persisted.DeletedIDs {
		l.deleted[id] = true
	}
	l.pathsRepaired = persisted.LibraryPathsRepaired
}

func (l *Library) storeEnvelopeLocked(m *manifestFile) {
	if len(l.owners) > 0 {
		m.RetentionOwners = make(map[string][]string, len(l.owners))
		for id, set := range l.owners {
			if _, ok := l.manifest[id]; !ok || len(set) == 0 {
				continue
			}
			list := make([]string, 0, len(set))
			for o := range set {
				list = append(list, o)
			}
			sort.Strings(list)
			m.RetentionOwners[id] = list
		}
	}
	if len(l.deleted) > 0 {
		m.DeletedIDs = make([]string, 0, len(l.deleted))
		for id := range l.deleted {
			m.DeletedIDs = append(m.DeletedIDs, id)
		}
		sort.Strings(m.DeletedIDs)
	}
	m.LibraryPathsRepaired = l.pathsRepaired
}

// AddOwner records owner as a retention owner of id (idempotent).
func (l *Library) AddOwner(id, owner string) error {
	return l.changeOwner(id, owner, true)
}

// RemoveOwner drops owner from id (no-op when absent or when id is gone).
func (l *Library) RemoveOwner(id, owner string) error {
	err := l.changeOwner(id, owner, false)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (l *Library) changeOwner(id, owner string, add bool) error {
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("%w: %q", ErrInvalidMediaID, id)
	}
	if strings.TrimSpace(owner) == "" {
		return errors.New("media library: empty retention owner")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.manifest[id]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	set := l.owners[id]
	if add == (set != nil && set[owner]) {
		return nil // already in the requested state
	}
	if add {
		if set == nil {
			set = map[string]bool{}
			l.owners[id] = set
		}
		set[owner] = true
	} else {
		delete(set, owner)
		if len(set) == 0 {
			delete(l.owners, id)
		}
	}
	if err := l.persistLocked(); err != nil {
		if add {
			delete(set, owner)
			if len(set) == 0 {
				delete(l.owners, id)
			}
		} else {
			if l.owners[id] == nil {
				l.owners[id] = map[string]bool{}
			}
			l.owners[id][owner] = true
		}
		return err
	}
	return nil
}

// OwnerCount is the number of retention owners of id.
func (l *Library) OwnerCount(id string) int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.owners[id])
}

// IsDeleted reports whether id was deleted explicitly.
func (l *Library) IsDeleted(id string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.deleted[id]
}

// SetLibraryPath records the entry's Library file path (D26). An empty path
// clears the association.
func (l *Library) SetLibraryPath(id, path string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.manifest[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if entry.libraryPath == path {
		return nil
	}
	previous := entry
	entry.libraryPath = path
	l.manifest[id] = entry
	if err := l.persistLocked(); err != nil {
		l.manifest[id] = previous
		return err
	}
	return nil
}

// IDsForLibraryPath returns the ids whose Library file is path, or lies
// inside path when path is a directory (dir true). Sorted.
func (l *Library) IDsForLibraryPath(path string, dir bool) []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	prefix := strings.TrimSuffix(path, "/") + "/"
	var out []string
	for id, e := range l.manifest {
		if e.libraryPath == "" {
			continue
		}
		if e.libraryPath == path || (dir && (strings.HasPrefix(e.libraryPath, prefix) || path == "" || path == ".")) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// MoveLibraryPath makes every association at from (or under it, for a
// directory) follow the file to to (D26: a rename or a same-workspace move
// keeps the association). An empty to clears them (a move to another
// workspace).
func (l *Library) MoveLibraryPath(from, to string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	prefix := strings.TrimSuffix(from, "/") + "/"
	changed := map[string]manifestEntry{}
	for id, e := range l.manifest {
		switch {
		case e.libraryPath == from:
			changed[id] = e
			e.libraryPath = to
		case strings.HasPrefix(e.libraryPath, prefix):
			changed[id] = e
			if to == "" {
				e.libraryPath = ""
			} else {
				e.libraryPath = strings.TrimSuffix(to, "/") + "/" + strings.TrimPrefix(e.libraryPath, prefix)
			}
		default:
			continue
		}
		l.manifest[id] = e
	}
	if len(changed) == 0 {
		return nil
	}
	if err := l.persistLocked(); err != nil {
		for id, e := range changed {
			l.manifest[id] = e
		}
		return err
	}
	return nil
}

// PublishWithID stores reader under a preallocated id with create-or-adopt
// semantics (FR-078 (2)): an entry with that id and the same SHA-256 is
// adopted as it is; a tombstoned id is refused with ErrDeletedByUser.
func (l *Library) PublishWithID(id, filename, wantSHA256 string, reader io.Reader) (string, gen.MediaLibraryEntry, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("%w: %q", ErrInvalidMediaID, id)
	}
	ref := "media://workspace/" + l.workspaceID + "/" + id
	l.mu.RLock()
	deleted := l.deleted[id]
	existing, exists := l.manifest[id]
	l.mu.RUnlock()
	if deleted {
		return ref, gen.MediaLibraryEntry{}, fmt.Errorf("%w: %s", ErrDeletedByUser, id)
	}
	if exists {
		if wantSHA256 != "" && existing.sha256 != wantSHA256 {
			return ref, gen.MediaLibraryEntry{}, fmt.Errorf("%w: %s", ErrContentMismatch, id)
		}
		return ref, existing.projection(), nil
	}
	ref, projection, err := l.uploadWithID(parsed, filename, gen.MediaLibraryEntrySourceUserUpload, true, reader)
	if err != nil {
		return ref, projection, err
	}
	if wantSHA256 != "" && projection.Sha256 != nil && *projection.Sha256 != wantSHA256 {
		if _, delErr := l.Delete(id); delErr != nil {
			return ref, projection, errors.Join(fmt.Errorf("%w: %s", ErrContentMismatch, id), delErr)
		}
		return ref, projection, fmt.Errorf("%w: %s", ErrContentMismatch, id)
	}
	return ref, projection, nil
}

// LibraryPathsRepaired reports whether the one-time association repair ran.
func (l *Library) LibraryPathsRepaired() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.pathsRepaired
}

// UnassociatedEntries returns the entries with no Library file association,
// for the one-time repair (A-14).
func (l *Library) UnassociatedEntries() []gen.MediaLibraryEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []gen.MediaLibraryEntry
	for id, e := range l.manifest {
		if e.libraryPath != "" {
			continue
		}
		if _, stranded := l.stranded[id]; stranded {
			continue
		}
		out = append(out, e.projection())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Id.String() < out[j].Id.String() })
	return out
}

// CompleteLibraryPathRepair records the given associations and marks the
// repair done, in one manifest write.
func (l *Library) CompleteLibraryPathRepair(paths map[string]string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	previous := map[string]manifestEntry{}
	for id, path := range paths {
		e, ok := l.manifest[id]
		if !ok || e.libraryPath != "" {
			continue
		}
		previous[id] = e
		e.libraryPath = path
		l.manifest[id] = e
	}
	wasRepaired := l.pathsRepaired
	l.pathsRepaired = true
	if err := l.persistLocked(); err != nil {
		for id, e := range previous {
			l.manifest[id] = e
		}
		l.pathsRepaired = wasRepaired
		return err
	}
	return nil
}
