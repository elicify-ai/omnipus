package omnpkg

// RollbackPlan carries the C1–C2 compensation steps (Rollback contract,
// revision 7: C3/C4 are retired — the agent is invisible until commit, so no
// reload step exists and a rollback answer never carries an activation).
// C1 is the two places membership/edges leave the core: remove X's team entry,
// then remove every delegation edge involving X. C2 is DiscardPending(X)'s two
// places: delete X.pending first, then delete agents/X/ with everything in it.
// A nil step function is a place with nothing to undo.
type RollbackPlan struct {
	// MembershipAttempted records that the membership write (E8) was attempted,
	// so C1 must run; a failure before E8 skips C1 entirely.
	MembershipAttempted  bool
	RemoveMembership     func() error // C1a: remove X's team entry
	RemoveEdges          func() error // C1b: remove every delegation edge involving X
	DiscardPendingRecord func() error // C2a: delete entities/agents/X.pending
	DiscardPendingFolder func() error // C2b: delete agents/X/ with everything inside it
}

// Compensation step names (details.failed_step) and leftover names
// (details.leftovers) of an incomplete rollback (FR-037).
const (
	StepC1 = "C1"
	StepC2 = "C2"

	LeftoverTeamEntry     = "team_entry"
	LeftoverDelegation    = "delegation_edges"
	LeftoverPendingRecord = "pending_record"
	LeftoverAgentFolder   = "agent_folder"
)

// CompensationResult reports a rollback outcome: Complete, the failed step
// ("C1" or "C2" — the only values) and the leftovers named
// (import_rollback_incomplete shape; FR-037). There is no activation field.
// Errors holds the step errors in the order they happened.
type CompensationResult struct {
	Complete   bool
	FailedStep string
	Leftovers  []string
	Errors     []error
}

// RunRollback (revision 7) runs C1 (only when MembershipAttempted) and then C2
// ALWAYS — a C1 failure (membership removal, or a partial C1 after the
// membership was removed) does not skip C2. Within each step a failed place
// stops the later places of the same step. FailedStep is the first failed
// step; leftovers are named either way (FR-037).
func RunRollback(plan RollbackPlan) CompensationResult {
	res := CompensationResult{Complete: true}
	fail := func(step string, err error, leftovers ...string) {
		if res.Complete {
			res.FailedStep = step
		}
		res.Complete = false
		res.Leftovers = append(res.Leftovers, leftovers...)
		res.Errors = append(res.Errors, err)
	}
	if plan.MembershipAttempted {
		if err := runPlace(plan.RemoveMembership); err != nil {
			fail(StepC1, err, LeftoverTeamEntry, LeftoverDelegation)
		} else if err := runPlace(plan.RemoveEdges); err != nil {
			fail(StepC1, err, LeftoverDelegation)
		}
	}
	if err := runPlace(plan.DiscardPendingRecord); err != nil {
		fail(StepC2, err, LeftoverPendingRecord, LeftoverAgentFolder)
	} else if err := runPlace(plan.DiscardPendingFolder); err != nil {
		fail(StepC2, err, LeftoverAgentFolder)
	}
	return res
}

func runPlace(step func() error) error {
	if step == nil {
		return nil
	}
	return step()
}
