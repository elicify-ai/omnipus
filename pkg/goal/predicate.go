// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"errors"
	"fmt"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ErrOwnerNotFound is returned by GetByOwner/GetActiveByOwner when no
// matching goal record exists for the given owner.
var ErrOwnerNotFound = errors.New("goal: no goal record for owner")

// errMultipleActiveGoalsForOwner is returned (wrapped) by GetActiveByOwner
// when more than one active goal record is found for the same owner — a
// programming error upstream (something created a second goal for an owner
// that should have at most one active goal at a time), reported rather than
// silently resolved to "the first one found".
var errMultipleActiveGoalsForOwner = errors.New("goal: multiple active goal records found for owner")

// IsActiveState reports whether s is generated.GoalStateActive. A tiny pure
// function, exported so a caller already holding a Goal's State in memory
// (without a Store to query) can apply the SAME predicate C-25 requires
// every "does an active goal exist" site to share.
func IsActiveState(s generated.GoalState) bool {
	return s == generated.GoalStateActive
}

// ListActive returns every currently-active goal record, across every
// owner kind (C-24/C-25's shared selector). This is the accessor the
// quiet-window sweep (pkg/agent/goal_loop.go::goalIdleExpirySweep, R-06/E8)
// and the active-goal admission counter (GOAL-FR-049, E11) both read from,
// replacing the pre-existing GoalCondition != "" selector over
// session.ListSessions() — neither of which this package's callers may
// re-implement independently (C-25: "the gateway counter would return zero
// forever after the field is gone" is exactly the silent-wrong-answer this
// shared accessor exists to prevent).
//
// This is a full List() scan filtered in memory (OQ-11: "I have specified
// the accessor S1 must expose but NOT how it is indexed" — the indexing
// question is left open by the joint delivery plan, and this wave
// deliberately does not speculate about it under load it cannot measure).
// A terminal goal record is retained forever until D14's retention sweep
// removes it, so this directory can grow without bound between sweeps; a
// future wave sizing this against real goal counts may need to replace the
// scan with a real index without changing this method's signature.
func (s *Store) ListActive() ([]Goal, error) {
	goals, _, err := s.List()
	if err != nil {
		return nil, fmt.Errorf("goal: list active: %w", err)
	}
	active := make([]Goal, 0, len(goals))
	for _, g := range goals {
		if IsActiveState(g.State) {
			active = append(active, g)
		}
	}
	return active, nil
}

// ListActiveByOwnerKind filters ListActive to one owner kind. The
// active-goal admission counter (GOAL-FR-049/D12, owned by E11) needs
// session-owned goals only: "the 'goal' counter returns the number of goal
// records with owner_kind == session and state == active ... task-owned
// goals are excluded by owner_kind" (R-22) — task-owned goals are exempt
// from the global active-loop cap.
func (s *Store) ListActiveByOwnerKind(kind generated.GoalOwnerKind) ([]Goal, error) {
	active, err := s.ListActive()
	if err != nil {
		return nil, err
	}
	out := make([]Goal, 0, len(active))
	for _, g := range active {
		if g.OwnerKind == kind {
			out = append(out, g)
		}
	}
	return out, nil
}

// GetActiveByOwner returns the single active goal record for the given
// owner, or a wrapped ErrOwnerNotFound if none is active.
//
// At most one goal may be active for a given owner at a time: a task-owned
// goal because Store.Create refuses a second Create for a task that already
// has a goal record (R-04, ErrOwnerAlreadyHasGoal) — task re-runs go
// through Reactivate on the SAME record instead; a session-owned goal
// because ADR-081 D1's instant activation only ever opens one /goal per
// session at a time, even though a session may accumulate several TERMINAL
// goal records across its life. Finding more than one active match is
// therefore reported as errMultipleActiveGoalsForOwner rather than silently
// resolved to "the first one found" — that would hide a real invariant
// violation upstream.
func (s *Store) GetActiveByOwner(ownerKind generated.GoalOwnerKind, ownerID string) (*Goal, error) {
	active, err := s.ListActive()
	if err != nil {
		return nil, err
	}
	var found *Goal
	for i := range active {
		if active[i].OwnerKind != ownerKind || active[i].OwnerID != ownerID {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("goal: get active by owner %s/%s: %w: goals %q and %q are both active",
				ownerKind, ownerID, errMultipleActiveGoalsForOwner, found.GoalID, active[i].GoalID)
		}
		g := active[i]
		found = &g
	}
	if found == nil {
		return nil, fmt.Errorf("goal: get active by owner %s/%s: %w", ownerKind, ownerID, ErrOwnerNotFound)
	}
	return found, nil
}

// ActiveGoalExists is C-25's single predicate: "a goal record exists for
// this owner with status active." It replaces the GoalCondition != "" check
// that today stands in for "does a goal exist" at (per C-25's count) five
// call sites in the pre-ADR-086 codebase — every one of those sites
// re-points to this method (or, in the gateway's admission-counter case
// which is owned by wave E11 rather than this one, to
// ListActiveByOwnerKind(generated.GoalOwnerKindSession) counting its
// result, per R-22).
func (s *Store) ActiveGoalExists(ownerKind generated.GoalOwnerKind, ownerID string) (bool, error) {
	_, err := s.GetActiveByOwner(ownerKind, ownerID)
	if err != nil {
		if errors.Is(err, ErrOwnerNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// GetByOwner returns the goal record for the given owner regardless of its
// current phase/state, or a wrapped ErrOwnerNotFound if none exists.
//
// This is safe to call unconditionally for a TASK-owned lookup: R-04
// guarantees at most one goal record ever exists for a given task owner,
// across its whole life (defining, active, and every terminal phase all
// share the one record). It is NOT generally safe for a session-owned
// lookup, since a session may accumulate several terminal goal records
// across its life — calling GetByOwner with a session owner that has more
// than one goal record on file returns an error rather than an arbitrary
// match; callers that want "the currently active one" for a session should
// use GetActiveByOwner instead.
func (s *Store) GetByOwner(ownerKind generated.GoalOwnerKind, ownerID string) (*Goal, error) {
	goals, _, err := s.List()
	if err != nil {
		return nil, fmt.Errorf("goal: get by owner: %w", err)
	}
	var found *Goal
	for i := range goals {
		if goals[i].OwnerKind != ownerKind || goals[i].OwnerID != ownerID {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("goal: get by owner %s/%s: found more than one goal record (%q and %q) — use GetActiveByOwner for a session-owned lookup",
				ownerKind, ownerID, found.GoalID, goals[i].GoalID)
		}
		g := goals[i]
		found = &g
	}
	if found == nil {
		return nil, fmt.Errorf("goal: get by owner %s/%s: %w", ownerKind, ownerID, ErrOwnerNotFound)
	}
	return found, nil
}
