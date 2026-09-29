// Omnipus — preflight for moving pipeline-owned view membership.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"fmt"
	"sort"
	"strings"
)

type viewMemberRename struct {
	oldBase, newBase string
	name             string
	oldRel, newRel   string
}

// planViewMemberRename maps only entries the move affects. It never infers
// ownership from a file's marker, and checks every affected current file
// before any record entry is revoked or any filesystem rename begins.
func (m *ViewMembership) planViewMemberRename(from, to string, folder bool) ([]viewMemberRename, error) {
	if from == to {
		return nil, nil
	}
	mapped := func(rel string) string {
		if folder {
			if strings.HasPrefix(rel, from+"/") {
				return to + strings.TrimPrefix(rel, from)
			}
			return rel
		}
		if rel == from {
			return to
		}
		return rel
	}
	var changed []viewMemberRename
	claimedPaths := make(map[string]string)
	claimedNames := make(map[string]string)
	for base, names := range m.Bases {
		newBase := mapped(base)
		for name, rel := range names {
			newRel := mapped(rel)
			if !validMembershipPath(newBase, ".base") || !validMembershipPath(newRel, ".view") {
				return nil, fmt.Errorf("knowledge: moving %q to %q changes a managed .base or .view file's type", from, to)
			}
			identity := newBase + "\x00" + name
			if owner, ok := claimedNames[identity]; ok {
				return nil, fmt.Errorf("knowledge: view %q would have two owners after move (%s, %s)", name, owner, rel)
			}
			claimedNames[identity] = rel
			if owner, ok := claimedPaths[newRel]; ok {
				return nil, fmt.Errorf("knowledge: path %q would have two membership claims (%s, %s)", newRel, owner, rel)
			}
			claimedPaths[newRel] = rel
			if newBase == base && newRel == rel {
				continue
			}
			changed = append(changed, viewMemberRename{base, newBase, name, rel, newRel})
		}
	}
	sort.Slice(changed, func(i, j int) bool {
		if changed[i].oldRel != changed[j].oldRel {
			return changed[i].oldRel < changed[j].oldRel
		}
		return changed[i].oldBase < changed[j].oldBase
	})
	verifiedBases := make(map[string]bool)
	for _, member := range changed {
		if member.oldBase != member.newBase && !verifiedBases[member.oldBase] {
			if err := m.VerifyBase(member.oldBase); err != nil {
				return nil, err
			}
			verifiedBases[member.oldBase] = true
		}
	}
	for _, member := range changed {
		if err := m.withManagedViewLock(member.oldRel, func() error {
			_, _, err := m.readManagedView(member.oldBase, member.name, member.oldRel)
			return err
		}); err != nil {
			return nil, fmt.Errorf("knowledge: cannot move unverified managed view %q: %w", member.oldRel, err)
		}
	}
	return changed, nil
}
