// Omnipus — refuse cross-collection transfers of pipeline-owned views.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// TrackedViewTransferError refuses a move that would strand trusted view
// membership outside the knowledge base that owns it.
type TrackedViewTransferError struct {
	Paths          []string
	PendingMoveIDs []string
}

func (e *TrackedViewTransferError) Error() string {
	message := fmt.Sprintf("knowledge: moving tracked derived view or its .base out of its knowledge base is refused; tracked paths: %s", strings.Join(e.Paths, ", "))
	if len(e.PendingMoveIDs) > 0 {
		message += "; pending move IDs: " + strings.Join(e.PendingMoveIDs, ", ")
	}
	return message
}

// RefuseTrackedTransfer applies the collection-departure rule to a source.
// It changes neither the membership record nor any file.
func (m *ViewMembership) RefuseTrackedTransfer(rel string, folder bool) error {
	paths, pendingIDs, err := m.trackedTransferPaths(rel, folder)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	return &TrackedViewTransferError{Paths: paths, PendingMoveIDs: pendingIDs}
}

// TrackedTransferPaths reports every tracked .base or derived view affected
// by taking rel outside this knowledge base. It is read-only: a refusal must
// not re-key or release membership. Callers hold the membership lock over both
// this preflight and the subsequent filesystem transfer.
func (m *ViewMembership) TrackedTransferPaths(rel string, folder bool) ([]string, error) {
	paths, _, err := m.trackedTransferPaths(rel, folder)
	return paths, err
}

func (m *ViewMembership) trackedTransferPaths(rel string, folder bool) ([]string, []string, error) {
	matches := func(candidate string) bool {
		return candidate == rel || folder && strings.HasPrefix(candidate, rel+"/")
	}
	found, pendingIDs := m.pendingTransferPaths(rel, matches)
	if len(m.Bases) == 0 {
		return sortedTrackedPaths(found), pendingIDs, nil
	}
	root, err := NewCollectionRoot(OSLinkFS(), m.Root)
	if err != nil {
		return nil, nil, err
	}
	set, report, err := LoadViewsForCollection(OSLinkFS(), root, nil)
	if err != nil {
		return nil, nil, err
	}
	views := make(map[string]*records.SavedView)
	for _, view := range set.Views() {
		views[view.Name()] = view
	}
	for base, names := range m.Bases {
		if matches(base) && len(names) != 0 {
			found[base] = true
		}
		for name, recorded := range names {
			current := recorded
			if view := views[name]; view != nil && view.Def.DerivedFrom != nil && *view.Def.DerivedFrom == base {
				candidate, pathErr := filepath.Rel(m.Root, view.SourcePath)
				if pathErr != nil {
					return nil, nil, pathErr
				}
				current = filepath.ToSlash(candidate)
				if !validMembershipPath(current, ".view") {
					return nil, nil, fmt.Errorf("knowledge: unsafe tracked view location %q", current)
				}
			}
			if matches(current) || matches(base) {
				found[current] = true
			}
			for _, rejection := range report.Rejections {
				if rejection.Name != name {
					continue
				}
				for _, abs := range rejection.Paths {
					candidate, pathErr := filepath.Rel(m.Root, abs)
					if pathErr != nil {
						return nil, nil, pathErr
					}
					path := filepath.ToSlash(candidate)
					if validMembershipPath(path, ".view") && matches(path) {
						found[path] = true
					}
				}
			}
		}
	}
	return sortedTrackedPaths(found), pendingIDs, nil
}
