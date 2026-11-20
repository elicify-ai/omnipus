package systools

// workspace_membership.go: joining and leaving a workspace team, shared by the
// create_agent tool, the agent delete cascade and the package import (epic
// #1094 T-4; spec export-import-s1 revision 7, FR-035, FR-037, FR-049; the
// architect's sign-off ARCH-SIGNOFF-T3-T4.md, T-4 block and C-4..C-7).

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

// ErrWorkspaceNotFound marks a join or leave whose workspace record does not
// exist. The import maps it to workspace_not_found.
var ErrWorkspaceNotFound = errors.New("workspace not found")

// WorkspaceMembershipOptions carries what *Deps supplies to the join and leave
// (sign-off C-4), plus test seams (C-5). The funcs are read at the moment the
// join reads them (under the workspace lock), so evaluation timing is the same
// as before the extraction. The seam fields are nil in production, except that
// the create_agent tool forwards its existing package seams.
type WorkspaceMembershipOptions struct {
	Home             string
	SelfEdgeExcluded func() map[string]bool // tool: selfEdgeExcludedAgentIDs(deps); gateway: cfg.SelfEdgeExcludeAgentIDSet()
	DepthCeiling     func() int             // tool: workspaceDelegationDepthCeiling(deps); gateway: delegationDepthCeiling(cfg)

	// Test seams. nil in production means the real function.
	SaveDelegation       func(home, wsID string, edges []workspacepkg.DelegationEdge) error // nil -> workspacepkg.SaveDelegation
	WriteWorkspace       func(dir, wsID string, w workspacepkg.Workspace) error             // nil -> writeEntity
	AfterMembershipWrite func(wsID, agentID string)                                         // called after the membership write, under the lock
}

func (o WorkspaceMembershipOptions) saveDelegation(wsID string, edges []workspacepkg.DelegationEdge) error {
	if o.SaveDelegation != nil {
		return o.SaveDelegation(o.Home, wsID, edges)
	}
	return workspacepkg.SaveDelegation(o.Home, wsID, edges)
}

func (o WorkspaceMembershipOptions) writeWorkspace(wsID string, w workspace) error {
	if o.WriteWorkspace != nil {
		return o.WriteWorkspace(workspacesDir(o.Home), wsID, w)
	}
	return writeEntity(workspacesDir(o.Home), wsID, w)
}

func (o WorkspaceMembershipOptions) selfEdgeExcluded() map[string]bool {
	if o.SelfEdgeExcluded == nil {
		return nil
	}
	return o.SelfEdgeExcluded()
}

func (o WorkspaceMembershipOptions) depthCeiling() int {
	if o.DepthCeiling == nil {
		return workspaceDelegationDepthCeilingFallback
	}
	return o.DepthCeiling()
}

// readMembershipWorkspace is readWorkspaceFromDisk plus ErrWorkspaceNotFound
// in the chain when the record does not exist. The caller holds
// workspacepkg.LockID(wsID).
func readMembershipWorkspace(home, wsID string) (workspace, error) {
	w, err := readWorkspaceFromDisk(home, wsID)
	if err == nil || validateID(wsID) != nil {
		return w, err
	}
	if _, statErr := os.Stat(entityPath(workspacesDir(home), wsID)); errors.Is(statErr, os.ErrNotExist) {
		return w, fmt.Errorf("%w: %w", ErrWorkspaceNotFound, err)
	}
	return w, err
}

// JoinWorkspaceTeam appends agentID to workspace wsID's core_team (deduped),
// then seeds agentID's ordinary self-delegation row, in one
// workspacepkg.LockID(wsID) section that is released when it returns. It is
// the create_agent tool's join, moved (see joinWorkspaceLocked). Results:
//
//	(true,  nil)  member, and the self-row is present (seeded now or already there)
//	(true,  err)  membership landed, but the re-read or the self-row seed failed
//	(false, err)  membership write failed (or the read before it)
//	(false, nil)  a competing writer removed the agent before the re-read; no seed
func JoinWorkspaceTeam(o WorkspaceMembershipOptions, wsID, agentID string) (joined bool, err error) {
	unlock := workspacepkg.LockID(wsID)
	defer unlock()
	joined, _, err = joinWorkspaceLocked(o, wsID, agentID)
	return joined, err
}

// joinWorkspaceLocked is the join's body. The caller holds
// workspacepkg.LockID(wsID); the lock pool is not reentrant, so nothing here
// takes it. wrote reports whether a membership write was attempted (the agent
// was not yet on the team), which is when a rollback must run the leave.
//
// U5A-FIX-R1 / N1: the authoritative membership read-and-decision and the
// self-row seed run in ONE workspace critical section. A concurrent explicit
// delegation graph PUT (which holds LockID and replaces the whole edge set)
// therefore lands wholly before or wholly after this join, never between, so
// a late implicit seed can never re-add a self-row the operator just excluded.
func joinWorkspaceLocked(o WorkspaceMembershipOptions, wsID, agentID string) (joined, wrote bool, err error) {
	w, err := readMembershipWorkspace(o.Home, wsID)
	if err != nil {
		return false, false, fmt.Errorf("read workspace %s: %w", wsID, err)
	}
	if !teamContains(w.CoreTeam, agentID) {
		wrote = true
		w.CoreTeam = append(w.CoreTeam, agentID)
		w.UpdatedAt = nowISO()
		if err := o.writeWorkspace(wsID, w); err != nil {
			return false, wrote, fmt.Errorf("write workspace %s: %w", wsID, err)
		}
	}

	// Deterministic test seam (nil in production): fires with LockID held,
	// AFTER the membership write and BEFORE the authoritative re-read, so a
	// test can land a competing membership/graph edit at exactly this point.
	if o.AfterMembershipWrite != nil {
		o.AfterMembershipWrite(wsID, agentID)
	}

	// Re-read the AUTHORITATIVE membership and seed ONLY when the agent is
	// genuinely on the team: a member removed by a competing writer gets no
	// self-row (seeding one would invent a delegation row for a non-member).
	fresh, readErr := readWorkspaceFromDisk(o.Home, wsID)
	if readErr != nil {
		// The membership write above already landed; only the confirm failed.
		return true, wrote, fmt.Errorf("re-read workspace %s membership: %w", wsID, readErr)
	}
	if !teamContains(fresh.CoreTeam, agentID) {
		return false, wrote, nil
	}

	// A failure here means the agent IS a workspace member but its required
	// self-row was not written: the caller reports the truthful partial result.
	if err := seedSelfEdgeLocked(o, wsID, agentID); err != nil {
		return true, wrote, err
	}
	return true, wrote, nil
}

// seedSelfEdgeLocked writes agentID's ordinary self-delegation row into
// workspace wsID's delegation store, preserving every existing edge. It uses
// the shared workspace.SelfEdgeSeedRows computation, so the row is identical to
// the install-seed and team-growth rows, and it threads the OPERATOR exclusion
// data (config.workspace_seed_defaults.self_edge.exclude_agent_ids) through
// o.SelfEdgeExcluded: the exclusion is config DATA, never a Go identity
// predicate. Idempotent: an agent already carrying a self-row is left
// untouched. The caller MUST already hold workspacepkg.LockID(wsID).
func seedSelfEdgeLocked(o WorkspaceMembershipOptions, wsID, agentID string) error {
	existing, ok := workspacepkg.LoadDelegation(o.Home, wsID)
	if !ok {
		return fmt.Errorf("workspace %s delegation record is unreadable — refusing to seed a self-row over it", wsID)
	}
	rows := workspacepkg.SelfEdgeSeedRows([]string{agentID}, existing, o.selfEdgeExcluded(), o.depthCeiling())
	if len(rows) == 0 {
		return nil
	}
	merged := append(append([]workspacepkg.DelegationEdge(nil), existing...), rows...)
	if err := o.saveDelegation(wsID, merged); err != nil {
		return fmt.Errorf("seed self-row for %s in workspace %s: %w", agentID, wsID, err)
	}
	return nil
}

// LeaveStage names the stage a leave failed at ("" on success).
type LeaveStage string

const (
	LeaveStageNone            LeaveStage = ""
	LeaveStageReadWorkspace   LeaveStage = "read_workspace"
	LeaveStageWriteMembership LeaveStage = "write_membership"
	LeaveStageReadDelegation  LeaveStage = "read_delegation"
	LeaveStageSaveDelegation  LeaveStage = "save_delegation"
)

// LeaveResult is the typed outcome of one leave.
type LeaveResult struct {
	MembershipRemoved bool       // this call removed agentID from core_team
	EdgesRemoved      int        // edges involving agentID removed from the delegation store
	WorkspaceMissing  bool       // the workspace record no longer exists (C-7)
	FailedStage       LeaveStage // "" on success
}

// LeaveWorkspaceTeam removes agentID from wsID's core_team, then (only when an
// edge involving agentID exists) saves the delegation store without every such
// edge. It re-reads both under one workspacepkg.LockID(wsID) section, so a
// competing writer's completed edit is kept. err != nil <=> FailedStage != "".
// Partial C1 is MembershipRemoved == true && FailedStage is read_delegation or
// save_delegation. A missing workspace record is WorkspaceMissing and not a
// failure (sign-off C-7).
func LeaveWorkspaceTeam(o WorkspaceMembershipOptions, wsID, agentID string) (LeaveResult, error) {
	unlock := workspacepkg.LockID(wsID)
	defer unlock()
	return leaveWorkspaceLocked(o, wsID, agentID)
}

// leaveWorkspaceLocked is the one leave core shared by LeaveWorkspaceTeam,
// JoinWorkspaceTeamAndPublish's rollback and the delete cascade
// (cascadeCleanOneWorkspaceLocked). The caller holds
// workspacepkg.LockID(wsID). Membership comes first; the delegation store is
// saved only when an edge involving agentID exists. Each failure wraps the
// failing step's own error exactly once (cascadeCleanOneWorkspaceLocked
// unwraps it to keep its warning text).
func leaveWorkspaceLocked(o WorkspaceMembershipOptions, wsID, agentID string) (LeaveResult, error) {
	var res LeaveResult
	w, err := readMembershipWorkspace(o.Home, wsID)
	switch {
	case errors.Is(err, ErrWorkspaceNotFound):
		// C-7: no record, so no membership to remove; edges are still
		// removed below when the store exists and is readable.
		res.WorkspaceMissing = true
	case err != nil:
		res.FailedStage = LeaveStageReadWorkspace
		return res, fmt.Errorf("read workspace %s: %w", wsID, err)
	case teamContains(w.CoreTeam, agentID):
		newTeam := make([]string, 0, len(w.CoreTeam))
		for _, member := range w.CoreTeam {
			if member != agentID {
				newTeam = append(newTeam, member)
			}
		}
		w.CoreTeam = newTeam
		w.UpdatedAt = nowISO()
		if err := o.writeWorkspace(wsID, w); err != nil {
			res.FailedStage = LeaveStageWriteMembership
			return res, fmt.Errorf("remove agent %s from workspace %s core_team: %w", agentID, wsID, err)
		}
		res.MembershipRemoved = true
	}

	edges, ok := workspacepkg.LoadDelegation(o.Home, wsID)
	if !ok {
		if res.WorkspaceMissing {
			// C-7: with the workspace gone, its team entry is empty and the
			// leave counts as done; the unreadable leftover store is reported.
			slog.Warn("systools: workspace leave: workspace record is gone and its delegation store is unreadable; edges were left in place",
				"workspace_id", wsID, "agent_id", agentID)
			return res, nil
		}
		res.FailedStage = LeaveStageReadDelegation
		return res, fmt.Errorf("workspace %s delegation store is unreadable", wsID)
	}
	var kept []workspacepkg.DelegationEdge
	removed := 0
	for _, edge := range edges {
		if edge.FromAgent == agentID || edge.ToAgent == agentID {
			removed++
			continue
		}
		kept = append(kept, edge)
	}
	if removed > 0 {
		if err := o.saveDelegation(wsID, kept); err != nil {
			res.FailedStage = LeaveStageSaveDelegation
			return res, fmt.Errorf("save workspace %s delegation edges: %w", wsID, err)
		}
		res.EdgesRemoved = removed
	}
	return res, nil
}

// JoinWorkspaceTeamAndPublish (spec revision 7, mechanism step 2) takes
// workspacepkg.LockID(wsID) once and, inside that one lock hold: re-reads W
// (missing -> failure), appends agentID to core_team and writes it, runs the
// AfterMembershipWrite hook, re-reads (agentID must be a member), seeds the
// self-row, then calls publish. If any of these fails after the membership
// write was attempted, it runs the leave core (C1) under the SAME lock hold,
// then releases the lock. The commit point is publish returning nil inside
// the helper. On success the LeaveResult is the zero value and the error nil;
// on failure the error is the failure itself and the LeaveResult is C1's
// outcome (what the leave managed to undo). A C1 failure is reported through
// LeaveResult.FailedStage and logged with its cause.
func JoinWorkspaceTeamAndPublish(o WorkspaceMembershipOptions, wsID, agentID string, publish func() error) (LeaveResult, error) {
	unlock := workspacepkg.LockID(wsID)
	defer unlock()

	joined, wrote, err := joinWorkspaceLocked(o, wsID, agentID)
	if err == nil && !joined {
		err = fmt.Errorf("agent %s was removed from workspace %s before the join completed", agentID, wsID)
	}
	if err == nil {
		if err = publish(); err == nil {
			return LeaveResult{}, nil
		}
	}
	if !wrote {
		// The read failed or the agent was already a member: this call
		// changed no membership, so there is nothing for C1 to undo.
		return LeaveResult{}, err
	}
	res, leaveErr := leaveWorkspaceLocked(o, wsID, agentID)
	if leaveErr != nil {
		slog.Error("systools: workspace join rollback (C1) failed",
			"workspace_id", wsID, "agent_id", agentID, "failed_stage", string(res.FailedStage),
			"membership_removed", res.MembershipRemoved, "error", leaveErr)
	}
	return res, err
}
