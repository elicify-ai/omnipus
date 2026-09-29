// Omnipus — pipeline-owned view membership, kept beside the collection index.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// ViewMembershipFileName names the derived provenance record outside the vault.
const ViewMembershipFileName = "view_membership.json"

const viewMembershipVersion = 1
const viewMembershipLockKey = ".omnipus-vault/view-membership.json"

// ViewMembership maps each .base path to the names and CURRENT paths of the
// view files its import/re-derivation pipeline actually wrote. File content
// alone never inserts an entry into this record.
type ViewMembership struct {
	Version        int                          `json:"version"`
	Root           string                       `json:"root"`
	Bases          map[string]map[string]string `json:"bases"`
	PendingMoves   map[string]PendingViewMove   `json:"pending_moves,omitempty"`
	CompletedMoves map[string]CompletedViewMove `json:"completed_moves,omitempty"`
	home           string
}

func newViewMembership(home, root string) *ViewMembership {
	return &ViewMembership{Version: viewMembershipVersion, Root: root,
		Bases: make(map[string]map[string]string), home: home}
}

// LoadViewMembership returns an empty record for an absent or unreadable file.
// A non-nil error also names the unreadable/corrupt reason; the caller must
// surface it, but must NEVER reconstruct membership from derived_from fields.
func LoadViewMembership(home, collectionRoot string) (*ViewMembership, error) {
	root, err := ResolveCollectionRoot(collectionRoot)
	if err != nil {
		return nil, err
	}
	empty := newViewMembership(home, root)
	dir, err := IndexDirFor(home, root)
	if err != nil {
		return empty, err
	}
	file := filepath.Join(dir, ViewMembershipFileName)
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, fmt.Errorf("knowledge: read view membership %s: %w", file, err)
	}
	var record ViewMembership
	if err := json.Unmarshal(data, &record); err != nil {
		return empty, fmt.Errorf("knowledge: parse view membership %s: %w", file, err)
	}
	if record.Version != viewMembershipVersion || record.Root != root {
		return empty, fmt.Errorf("knowledge: invalid version or collection root in view membership %s", file)
	}
	seenPaths := make(map[string]string)
	for base, names := range record.Bases {
		if !validMembershipPath(base, ".base") || names == nil {
			return empty, fmt.Errorf("knowledge: invalid base in view membership %s: %q", file, base)
		}
		for name, view := range names {
			if strings.TrimSpace(name) == "" || !validMembershipPath(view, ".view") {
				return empty, fmt.Errorf("knowledge: invalid view in membership %s: %q", file, view)
			}
			if owner, exists := seenPaths[view]; exists {
				return empty, fmt.Errorf("knowledge: view %q has two membership claims (%s, %s)", view, owner, base)
			}
			seenPaths[view] = base
		}
	}
	for id, pending := range record.PendingMoves {
		if err := pending.validate(root, id); err != nil {
			return empty, err
		}
	}
	for id, done := range record.CompletedMoves {
		if id == "" || done.From == "" || done.To == "" || done.CompletedAt.IsZero() {
			return empty, fmt.Errorf("knowledge: invalid completed view move %q", id)
		}
		if _, pending := record.PendingMoves[id]; pending {
			return empty, fmt.Errorf("knowledge: view move %q is both pending and completed", id)
		}
	}
	if record.Bases == nil {
		record.Bases = make(map[string]map[string]string)
	}
	record.home = home
	return &record, nil
}

func validMembershipPath(rel, suffix string) bool {
	return rel != "" && !strings.Contains(rel, "\\") &&
		!IsAbsoluteTarget(rel) && path.Clean(rel) == rel &&
		!strings.HasPrefix(rel, "../") && rel != ".." &&
		strings.EqualFold(path.Ext(rel), suffix)
}

// SaveViewMembership persists the trusted record atomically and privately.
// Only a pipeline write or a Library lifecycle update should mutate it.
func SaveViewMembership(record *ViewMembership) error {
	if record == nil {
		return fmt.Errorf("knowledge: no view membership to save")
	}
	dir, err := IndexDirFor(record.home, record.Root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("knowledge: create view membership directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("knowledge: secure view membership directory: %w", err)
	}
	record.Version = viewMembershipVersion
	if record.Bases == nil {
		record.Bases = make(map[string]map[string]string)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("knowledge: encode view membership: %w", err)
	}
	file := filepath.Join(dir, ViewMembershipFileName)
	if err := fileutil.WriteFileAtomic(file, data, 0o600); err != nil {
		return fmt.Errorf("knowledge: write view membership %s: %w", file, err)
	}
	if err := os.Chmod(file, 0o600); err != nil {
		return fmt.Errorf("knowledge: secure view membership %s: %w", file, err)
	}
	return nil
}

// WithViewMembershipLock serializes membership changes across the importer,
// re-deriver, and Library lifecycle doors. Callers re-read the record while
// holding this lock and save it before releasing the lock.
func WithViewMembershipLock(home, collectionRoot string, fn func() error) error {
	root, err := ResolveCollectionRoot(collectionRoot)
	if err != nil {
		return err
	}
	lockDir, err := LockDirFor(home, root)
	if err != nil {
		return err
	}
	return withExactViewMembershipLock(root, lockDir, fn)
}

// WithViewMembership loads a record under its lock for a Library lifecycle
// mutation. A corrupt/unreadable record is not trusted and blocks the mutation;
// re-derivation instead handles that case as empty and reports the warning.
func WithViewMembership(home, collectionRoot string, fn func(*ViewMembership) error) error {
	return WithViewMembershipLock(home, collectionRoot, func() error {
		record, err := LoadViewMembership(home, collectionRoot)
		if err != nil {
			return err
		}
		return fn(record)
	})
}
