// Omnipus — safe-by-refusal ownership changes during Library and agent moves.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"fmt"
	"log/slog"
)

// ViewMembershipMoveIncompleteError means the rename started after its old
// membership was revoked, but the new ownership could not be confirmed. The
// views remain on disk; a later import may make a suffixed copy rather than
// treating their editable provenance as a way to recover ownership.
type ViewMembershipMoveIncompleteError struct {
	From, To string
	RetryID  string
	Cause    error
}

func (e *ViewMembershipMoveIncompleteError) Error() string {
	return fmt.Sprintf("knowledge: view membership update incomplete for %q -> %q: %v; affected views are no longer managed by their .base; Retry / re-enroll ID: %s", e.From, e.To, e.Cause, e.RetryID)
}

func (e *ViewMembershipMoveIncompleteError) Unwrap() error { return e.Cause }

// RenameWithViewMembership is the shared Library/agent move door. The
// collection membership lock is held before Renamer takes individual file
// locks, and a recorded file is verified before its membership is revoked.
// Revoking BEFORE the filesystem move matters: if the move lands but saving a
// new record fails, the old path must not remain an authorized delete target
// for a copy later placed there with the same name and derived_from marker.
func RenameWithViewMembership(home string, renamer *Renamer, req RenameRequest) (*RenameResult, error) {
	if renamer == nil || !renamer.Root.Valid() {
		return nil, fmt.Errorf("knowledge: rename requires a configured collection root")
	}
	var result *RenameResult
	err := WithViewMembership(home, renamer.Root.Path(), func(m *ViewMembership) error {
		if reconcileErr := m.ReconcileDiscoveredViewPaths(); reconcileErr != nil {
			return reconcileErr
		}
		from, to := normalizeRel(req.From), normalizeRel(req.To)
		changed, planErr := m.planViewMemberRename(from, to, req.Folder)
		if planErr != nil {
			return planErr
		}
		if len(changed) == 0 {
			result, planErr = renamer.Rename(req)
			return planErr
		}
		// A routine refusal must leave the existing ownership intact. The
		// renamer will re-plan after revocation; any intervening failure is
		// reported as incomplete rather than silently re-enrolling old paths.
		if _, planErr = renamer.Plan(req); planErr != nil {
			renamer.emit(RenameAuditEvent{From: from, To: to, Outcome: RenameOutcomeRefused,
				Reason: planErr.Error(), Paths: sortedUnique(from, to)})
			return planErr
		}
		pendingID := m.stagePendingMove(req, changed)
		for _, member := range changed {
			delete(m.Bases[member.oldBase], member.name)
			if len(m.Bases[member.oldBase]) == 0 {
				delete(m.Bases, member.oldBase)
			}
		}
		// One atomic record write publishes the trusted retry plan and
		// revokes every old path. There is no window where a revoked
		// membership can be mistaken for an unrecorded recovery claim.
		if saveErr := SaveViewMembership(m); saveErr != nil {
			return fmt.Errorf("knowledge: cannot revoke view membership; rename not started: %w", saveErr)
		}
		result, planErr = renamer.Rename(req)
		if planErr != nil {
			return &ViewMembershipMoveIncompleteError{From: from, To: to, RetryID: pendingID, Cause: planErr}
		}
		if applyErr := m.enrollMovedMembers(changed, pendingID, from, to); applyErr != nil {
			return &ViewMembershipMoveIncompleteError{From: from, To: to, RetryID: pendingID, Cause: applyErr}
		}
		return nil
	})
	if incomplete, ok := err.(*ViewMembershipMoveIncompleteError); ok {
		slog.Warn("knowledge: rename left view membership incomplete", "from", incomplete.From,
			"to", incomplete.To, "error", incomplete.Cause)
	}
	return result, err
}

// enrollMovedMembers first confirms every moved file at its NEW path and
// rewrites provenance for moved bases under that file's own write lock. The
// record is enrolled only after ALL affected files verify; a failed write
// cannot leave a path in the record with stale or mismatched provenance.
func (m *ViewMembership) enrollMovedMembers(changed []viewMemberRename, pendingID, from, to string) error {
	root, err := NewCollectionRoot(OSLinkFS(), m.Root)
	if err != nil {
		return err
	}
	_, report, err := LoadViewsForCollection(OSLinkFS(), root, nil)
	if err != nil {
		return err
	}
	rejected := make(map[string]bool)
	for _, name := range report.RejectedNames() {
		rejected[name] = true
	}
	for _, member := range changed {
		if rejected[member.name] {
			return fmt.Errorf("knowledge: moved view %q has duplicate or rejected claimants", member.name)
		}
		if existing, ok := m.Bases[member.newBase][member.name]; ok && existing != member.newRel {
			return fmt.Errorf("knowledge: moved view %q conflicts with enrolled path %q", member.name, existing)
		}
		for base, names := range m.Bases {
			for name, path := range names {
				if path == member.newRel && (base != member.newBase || name != member.name) {
					return fmt.Errorf("knowledge: moved view path %q belongs to %q/%q", member.newRel, base, name)
				}
			}
		}
	}
	for _, member := range changed {
		if err := m.withManagedViewLock(member.newRel, func() error {
			// A prior attempt may have rewritten this marker before its
			// final membership save failed. Retry must accept that exact
			// verified result without rewriting it a second time.
			if _, _, err := m.readManagedView(member.newBase, member.name, member.newRel); err == nil {
				return nil
			}
			if member.oldBase == member.newBase {
				return fmt.Errorf("knowledge: moved view %q no longer matches its identity", member.newRel)
			}
			_, body, err := m.readManagedView(member.oldBase, member.name, member.newRel)
			if err != nil {
				return err
			}
			body, err = rewriteDerivedBase(body, member.oldBase, member.newBase)
			if err != nil {
				return err
			}
			if err := m.writeManagedViewBytes(member.newRel, body); err != nil {
				return err
			}
			_, _, err = m.readManagedView(member.newBase, member.name, member.newRel)
			return err
		}); err != nil {
			return fmt.Errorf("knowledge: cannot enroll moved view %q at %q: %w", member.name, member.newRel, err)
		}
	}
	for _, member := range changed {
		if m.Bases[member.newBase] == nil {
			m.Bases[member.newBase] = make(map[string]string)
		}
		m.Bases[member.newBase][member.name] = member.newRel
	}
	m.finishPendingMove(pendingID, from, to)
	return SaveViewMembership(m)
}
