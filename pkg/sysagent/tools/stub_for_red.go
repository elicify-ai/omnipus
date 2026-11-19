// stub_for_red.go — compile-only RED stubs for epic #1094 slice S1a tasks T-3
// and T-4 in this package.
//
// DELETE when GREEN lands. Every symbol here returns errors.New("not
// implemented") or a zero value so the RED tests in
// workspace_membership_red_test.go and agent_create_core coverage fail on an
// assertion or a not-implemented error — never on an undefined symbol. The
// shapes are the architect's sign-off (ARCH-SIGNOFF-T3-T4.md, T-4 block) as
// amended by spec revision 7 (JoinWorkspaceTeamAndPublish commits inside the
// helper's one workspace-lock hold; C1 runs there too).
package systools

import (
	"errors"

	"github.com/elicify-ai/omnipus/pkg/config"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

// WorkspaceMembershipOptions carries what *Deps supplies today, plus test
// seams (sign-off C-4/C-5). The funcs are read at the same moment the current
// code reads them (under the lock), so evaluation timing is unchanged. The
// seam fields are nil in production — production forwards the tool's existing
// package var for SaveDelegation only.
type WorkspaceMembershipOptions struct {
	Home             string
	SelfEdgeExcluded func() map[string]bool // tool: selfEdgeExcludedAgentIDs(deps); gateway: cfg.SelfEdgeExcludeAgentIDSet()
	DepthCeiling     func() int             // tool: workspaceDelegationDepthCeiling(deps); gateway: delegationDepthCeiling(cfg)

	// Test seams. nil in production means the real function.
	SaveDelegation       func(home, wsID string, edges []workspacepkg.DelegationEdge) error // nil -> workspacepkg.SaveDelegation
	WriteWorkspace       func(dir, wsID string, w workspacepkg.Workspace) error             // nil -> writeEntity
	AfterMembershipWrite func(wsID, agentID string)                                         // replaces joinWorkspaceMembershipTestHook's body
}

// JoinWorkspaceTeam is today's AgentCreateTool.joinWorkspaceTeam moved,
// unchanged. Same tuple, same meanings:
//
//	(true,  nil)  member, and the self-row is present (seeded now or already there)
//	(true,  err)  membership landed, but the re-read or the self-row seed failed
//	(false, err)  membership write failed (or the read before it)
//	(false, nil)  a competing writer removed the agent before the re-read; no seed
//
// The lock is released when it returns.
func JoinWorkspaceTeam(o WorkspaceMembershipOptions, wsID, agentID string) (joined bool, err error) {
	return false, errors.New("not implemented")
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
// save_delegation.
func LeaveWorkspaceTeam(o WorkspaceMembershipOptions, wsID, agentID string) (LeaveResult, error) {
	return LeaveResult{}, errors.New("not implemented")
}

// JoinWorkspaceTeamAndPublish (spec revision 7) takes workspacepkg.LockID(wsID)
// once and, inside that one lock hold: re-reads W (missing -> failure), appends
// agentID to core_team and writes it, runs the AfterMembershipWrite hook,
// re-reads (agentID must be a member), seeds the self-row, then calls publish.
// If any of these fails it runs the private leave core under the SAME lock
// hold, then releases the lock. The commit point is publish returning nil
// inside the helper. On success the returned LeaveResult is the zero value and
// the error nil; on failure the error is the failure and the LeaveResult is C1's
// outcome (what the leave managed to undo, for import_rollback_incomplete
// reporting).
func JoinWorkspaceTeamAndPublish(o WorkspaceMembershipOptions, wsID, agentID string, publish func() error) (LeaveResult, error) {
	return LeaveResult{}, errors.New("not implemented")
}

// ApplySharedConfigArgs is applySharedConfigArgs exported unchanged (T-3):
// import stores voice, memory_enabled and context_window_override with the
// create_agent tool's rule.
func ApplySharedConfigArgs(a *config.AgentConfig, args map[string]any) error {
	return errors.New("not implemented")
}

// AgentCreateArgNames returns a sorted copy of agentCreateArgNames (sign-off
// C-8), for TestImportFieldCoverage.
func AgentCreateArgNames() []string { return nil } // RED stub: not implemented

// AgentUpdateArgNames returns a sorted copy of agentUpdateArgNames (C-8).
func AgentUpdateArgNames() []string { return nil } // RED stub: not implemented
