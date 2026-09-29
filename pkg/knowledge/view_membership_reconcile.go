// Omnipus — reconcile pipeline-owned view identity after an external move.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ReconcileManagedViewPaths locates already-enrolled (base, name) identities in
// the collection's ONE discovery result. Discovery never creates membership:
// a unique claimant can only update the path of an existing record entry.
// Duplicate/rejected names refuse rather than selecting either file. Callers
// hold the collection membership lock while scanning and reconciling.
func (m *ViewMembership) ReconcileManagedViewPaths(set *records.ViewSet, report *records.ViewLoadReport) error {
	if set == nil || report == nil {
		return fmt.Errorf("knowledge: view reconciliation requires a discovery result")
	}
	rejected := make(map[string]bool)
	for _, name := range report.RejectedNames() {
		rejected[name] = true
	}
	var unknownClaims []string
	for _, rejection := range report.Rejections {
		if rejection.Name == "" {
			unknownClaims = append(unknownClaims, rejection.String())
		}
	}
	seen := make(map[string]*records.SavedView)
	for _, view := range set.Views() {
		seen[view.Name()] = view
	}
	type update struct{ base, name, rel string }
	var updates []update
	for base, names := range m.Bases {
		for _, name := range sortedManagedNames(names) {
			if rejected[name] {
				return fmt.Errorf("knowledge: managed view %q has a duplicate or rejected definition; neither claimant can be changed", name)
			}
			old := names[name]
			view := seen[name]
			if view != nil && view.Def.DerivedFrom != nil && *view.Def.DerivedFrom == base {
				rel, err := filepath.Rel(m.Root, view.SourcePath)
				if err != nil {
					return fmt.Errorf("knowledge: locate managed view %q: %w", name, err)
				}
				rel = filepath.ToSlash(rel)
				if !validMembershipPath(rel, ".view") {
					return fmt.Errorf("knowledge: managed view %q moved outside its collection: %q", name, rel)
				}
				if err := m.withManagedViewLock(rel, func() error {
					_, _, err := m.readManagedView(base, name, rel)
					return err
				}); err != nil {
					return fmt.Errorf("knowledge: verify unique managed view %q at %q: %w", name, rel, err)
				}
				if rel != old {
					updates = append(updates, update{base, name, rel})
				}
				continue
			}
			// An unreadable or mismatched file at its recorded path is not
			// evidence that the identity has been removed. Only a missing file
			// with no claimant elsewhere can retire its stale membership.
			err := m.withManagedViewLock(old, func() error {
				_, _, readErr := m.readManagedView(base, name, old)
				return readErr
			})
			switch {
			case err == nil:
				// A transient discovery skip cannot revoke a still-present view.
			case errors.Is(err, fs.ErrNotExist):
				if len(unknownClaims) != 0 {
					// A moved claimant may be inside an unreadable subtree or
					// an unparseable file. Discovery cannot prove retirement.
					slog.Warn("knowledge: view discovery incomplete; retained managed membership",
						"base", base, "view", name, "recorded_path", old,
						"unreadable_paths", unknownClaims)
					continue
				}
				updates = append(updates, update{base, name, ""})
			default:
				return fmt.Errorf("knowledge: cannot reconcile managed view %q at %q: %w", name, old, err)
			}
		}
	}
	if len(updates) == 0 {
		return nil
	}
	for _, u := range updates {
		if u.rel == "" {
			delete(m.Bases[u.base], u.name)
			if len(m.Bases[u.base]) == 0 {
				delete(m.Bases, u.base)
			}
		} else {
			m.Bases[u.base][u.name] = u.rel
		}
	}
	return SaveViewMembership(m)
}

// ReconcileDiscoveredViewPaths uses the collection's canonical view loader
// for Library/agent lifecycle operations without a previously loaded set.
func (m *ViewMembership) ReconcileDiscoveredViewPaths() error {
	if len(m.Bases) == 0 {
		return nil
	}
	root, err := NewCollectionRoot(OSLinkFS(), m.Root)
	if err != nil {
		return err
	}
	set, report, err := LoadViewsForCollection(OSLinkFS(), root, nil)
	if err != nil {
		return err
	}
	return m.ReconcileManagedViewPaths(set, report)
}
