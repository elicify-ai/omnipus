// Omnipus — block transfers while a trusted rename is still pending.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"sort"
	"strings"
	"time"
)

func sortedTrackedPaths(found map[string]bool) []string {
	out := make([]string, 0, len(found))
	for rel := range found {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// pendingTransferPaths considers both the old and planned new locations: a
// rename can have landed on disk while the record is still pending. Expired
// plans grant no authority and do not block an unrelated later move.
func (m *ViewMembership) pendingTransferPaths(rel string, matches func(string) bool) (map[string]bool, []string) {
	found := make(map[string]bool)
	var ids []string
	now := time.Now()
	for id, pending := range m.PendingMoves {
		if !now.Before(pending.StartedAt.Add(viewMoveRetryLifetime)) {
			continue
		}
		paths := []string{pending.From, pending.To}
		for _, member := range pending.Members {
			paths = append(paths, member.OldBase, member.NewBase, member.OldPath, member.NewPath)
		}
		matched := false
		for _, candidate := range paths {
			if candidate == "" {
				continue
			}
			insidePendingFolder := pending.Folder && (candidate == pending.From || candidate == pending.To) &&
				strings.HasPrefix(rel, candidate+"/")
			if matches(candidate) || insidePendingFolder {
				found[candidate] = true
				matched = true
			}
		}
		if matched {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return found, ids
}

// RefuseTrackedRootMove keeps the absolute-path-keyed record reachable. Even
// with every active entry revoked, an unexpired pending plan still needs this
// exact collection root for Retry.
func (m *ViewMembership) RefuseTrackedRootMove() error {
	found, ids := m.pendingTransferPaths("", func(string) bool { return true })
	for base, names := range m.Bases {
		if len(names) == 0 {
			continue
		}
		found[base] = true
		for _, rel := range names {
			found[rel] = true
		}
	}
	if len(found) == 0 {
		return nil
	}
	return &TrackedViewTransferError{Paths: sortedTrackedPaths(found), PendingMoveIDs: ids}
}
