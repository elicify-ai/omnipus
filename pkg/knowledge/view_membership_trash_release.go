// Omnipus — two-phase revoke and marker release for trashed views.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
)

type viewTrashMember struct {
	base, name, rel string
}

// planTrashRelease verifies every affected recorded member before any root is
// revoked. A trashed view is released even if its .base stays outside the
// folder; a trashed .base releases all of its views, including those outside.
func (m *ViewMembership) planTrashRelease(rel string, folder bool) ([]viewTrashMember, error) {
	if err := m.ReconcileDiscoveredViewPaths(); err != nil {
		return nil, err
	}
	matches := func(candidate string) bool {
		return rel == "" || candidate == rel || folder && strings.HasPrefix(candidate, rel+"/")
	}
	pending, ids := m.pendingTransferPaths(rel, matches)
	if len(ids) > 0 {
		return nil, &TrackedViewTransferError{Paths: sortedTrackedPaths(pending), PendingMoveIDs: ids}
	}
	var planned []viewTrashMember
	for base, names := range m.Bases {
		for name, current := range names {
			if matches(base) || matches(current) {
				planned = append(planned, viewTrashMember{base: base, name: name, rel: current})
			}
		}
	}
	sort.Slice(planned, func(i, j int) bool {
		if planned[i].base != planned[j].base {
			return planned[i].base < planned[j].base
		}
		return planned[i].name < planned[j].name
	})
	for _, member := range planned {
		err := m.withManagedViewLock(member.rel, func() error {
			_, _, readErr := m.readManagedView(member.base, member.name, member.rel)
			return readErr
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("knowledge: verify trash member %q: %w", member.rel, err)
		}
	}
	return planned, nil
}

// revokeTrashMembers makes the record save before marker stripping or trash.
// The caller holds all affected roots' membership locks across every phase.
func (m *ViewMembership) revokeTrashMembers(planned []viewTrashMember) error {
	if len(planned) == 0 {
		return nil
	}
	for _, member := range planned {
		delete(m.Bases[member.base], member.name)
		if len(m.Bases[member.base]) == 0 {
			delete(m.Bases, member.base)
		}
	}
	if err := SaveViewMembership(m); err != nil {
		return fmt.Errorf("knowledge: revoke trash membership: %w", err)
	}
	return nil
}

func (m *ViewMembership) stripTrashMemberMarkers(planned []viewTrashMember) error {
	for _, member := range planned {
		err := m.withManagedViewLock(member.rel, func() error {
			_, body, readErr := m.readManagedView(member.base, member.name, member.rel)
			if errors.Is(readErr, fs.ErrNotExist) {
				return nil
			}
			if readErr != nil {
				return readErr
			}
			released, stripErr := records.StripCopiedViewProvenance(body)
			if stripErr != nil {
				return stripErr
			}
			return m.writeManagedViewBytes(member.rel, released)
		})
		if err != nil {
			return fmt.Errorf("knowledge: strip released view %q: %w", member.rel, err)
		}
	}
	return nil
}
