// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_packages_apply.go: POST /api/v1/packages/import/{staging_id}/apply
// (epic #1094 S1a T-5; spec revision 7 FR-030–FR-041, FR-049, "Rollback
// contract"). New mode only. The agent is written as a pending record (E4),
// joined and published under one workspace lock (E8, E9, the commit rename),
// then published live (P1–P4). A failure before the commit point is undone
// (C1 inside the helper's lock hold, then C2, always).

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// errPackageCrashSeam marks an apply stopped by the crash seam (tests only).
var errPackageCrashSeam = errors.New("apply stopped at the crash seam")

const (
	packageApplyPrefix = "/api/v1/packages/import/"
	packageApplySuffix = "/apply"
)

// handlePackageImportApply is POST /api/v1/packages/import/{staging_id}/apply
// (FR-030, FR-037). The staging ID is the path segment between "import/" and
// "/apply"; it must be a staging UUID, so a traversal ID never reaches the disk.
func (a *restAPI) handlePackageImportApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	stagingID, ok := packageApplyStagingID(r.URL.Path)
	if !ok {
		writePackageError(w, packageErr(omnpkg.CodeStagingNotFound), nil)
		return
	}
	var req gen.PackageImportApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			jsonErr(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		jsonErrField(w, http.StatusBadRequest, "invalid JSON body: "+err.Error(), "mode")
		return
	}
	if !req.Mode.Valid() {
		jsonErrField(w, http.StatusBadRequest, `mode is required and must be "new", "replace" or "merge"`, "mode")
		return
	}
	reg := a.packageRegistry()
	if err := reg.BeginApply(stagingID); err != nil {
		writePackageErr(w, "apply", err)
		return
	}
	if req.Mode != gen.PackageImportApplyRequestModeNew {
		a.abortPackageApply(stagingID)
		writePackageError(w, packageErr(packageCodeModeNotAvailable, "mode", string(req.Mode)), nil)
		return
	}
	run := &packageApplyRun{a: a, stagingID: stagingID, actor: a.packageActor(r)}
	if err := run.prepare(req.WorkspaceId); err != nil {
		a.abortPackageApply(stagingID)
		writePackageErr(w, "apply", err)
		return
	}
	run.execute(w)
}

// packageApplyStagingID extracts and validates the staging ID of an apply path.
func packageApplyStagingID(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, packageApplyPrefix)
	if !ok {
		return "", false
	}
	id, ok := strings.CutSuffix(rest, packageApplySuffix)
	if !ok {
		return "", false
	}
	if _, err := uuid.Parse(id); err != nil || strings.ContainsAny(id, `/\.`) {
		return "", false
	}
	return id, true
}

// abortPackageApply returns an applying staging to ready after a refusal that
// wrote nothing (Staging lifecycle "End"; the expiry clock is unchanged).
func (a *restAPI) abortPackageApply(stagingID string) {
	if err := a.packageRegistry().AbortApply(stagingID); err != nil {
		slog.Warn("packages: return staging to ready", "staging_id", stagingID, "error", err)
	}
}

// packageApplyRun is one New-mode apply.
type packageApplyRun struct {
	a           *restAPI
	stagingID   string
	stagingDir  string
	actor       string
	workspaceID string
	sourceID    string
	plan        *packageAgentPlan
	warnings    []packageWarning
	notDurable  bool
}

func (p *packageApplyRun) agentID() string { return p.plan.Create.Agent.ID }

// prepare re-runs every check on the current state before the first write
// (provisional preview, FR-030): the package, the workspace, the name and the
// create rules. It writes nothing; any error leaves the staging ready.
func (p *packageApplyRun) prepare(workspaceID *string) error {
	dir, err := p.a.packageRegistry().Dir(p.stagingID)
	if err != nil {
		return err
	}
	p.stagingDir = dir
	m, payload, err := readStagedPackage(filepath.Join(dir, packageFileInStaging))
	if err != nil {
		return err
	}
	if p.workspaceID, err = p.a.packageTargetWorkspace(workspaceID); err != nil {
		return err
	}
	el := m.Elements[0]
	p.sourceID = el.SourceID
	if p.plan, err = p.a.planPackageAgent(el, payload, p.a.liveAgentNames(), false); err != nil {
		return err
	}
	p.warnings = append(p.warnings, p.plan.Warnings...)
	if proposed, ok := p.a.proposedName(p.stagingID); ok && proposed != p.plan.Name {
		p.warnings = append(p.warnings, packageWarning{
			Code: packageCodeChangedSincePreview, ElementSourceID: el.SourceID,
			Message: fmt.Sprintf("The name changed since the preview: %q is now taken, so the agent is named %q.", proposed, p.plan.Name),
			Items:   []string{"name"},
		})
	}
	taken := map[string]bool{el.SourceID: true}
	for _, ac := range p.a.agentLoop.GetConfig().Agents.List {
		taken[ac.ID] = true
	}
	id, err := omnpkg.FreshAgentID(taken)
	if err != nil {
		return err
	}
	p.plan.Create.Agent.ID = id
	return nil
}

// packageTargetWorkspace resolves the destination: the named workspace (404
// when unknown) or the default one (409 when there is none), FR-035.
func (a *restAPI) packageTargetWorkspace(id *string) (string, error) {
	if id != nil && *id != "" {
		if _, err := readWorkspaceFile(a.homePath, *id); err != nil {
			if errors.Is(err, errWorkspaceNotFound) {
				return "", packageErr(packageCodeWorkspaceNotFound, "workspace_id", *id)
			}
			return "", err
		}
		return *id, nil
	}
	list, err := listWorkspaceFiles(a.homePath)
	if err != nil {
		return "", fmt.Errorf("list workspaces: %w", err)
	}
	for _, ws := range list {
		if ws.IsDefault {
			return ws.ID, nil
		}
	}
	return "", packageErr(packageCodeDefaultWSMissing)
}

func (p *packageApplyRun) journal(state omnpkg.JournalState) error {
	return omnpkg.WriteJournal(p.stagingDir, omnpkg.JournalFile{
		State: state, AgentID: p.agentID(), StagingID: p.stagingID, WorkspaceID: p.workspaceID,
	})
}

// execute runs E1 … P4 and writes the answer.
func (p *packageApplyRun) execute(w http.ResponseWriter) {
	if err := p.journal(omnpkg.JournalStarted); err != nil { // E1
		_ = os.Remove(filepath.Join(p.stagingDir, omnpkg.JournalName))
		p.a.abortPackageApply(p.stagingID)
		writePackageErr(w, "apply", fmt.Errorf("write import journal: %w", err))
		return
	}
	if p.crashAt(w, "E1") {
		return
	}
	if err := p.createPending(); err != nil { // E2 + E4
		var refusal *requestRefusalError
		var gaps *toolPolicyCoverageGapError
		if errors.As(err, &refusal) || errors.As(err, &gaps) {
			// Refused before any write: the staging stays usable.
			_ = os.Remove(filepath.Join(p.stagingDir, omnpkg.JournalName))
			p.a.abortPackageApply(p.stagingID)
			writePackageError(w, notCreatable(refusalField(refusal), err.Error()), nil)
			return
		}
		p.rollback(w, err, nil)
		return
	}
	if p.crashAt(w, "E4") {
		return
	}
	if packageT5.CrashAt == "membership" {
		p.rollback(w, errors.New("fault before the join (crash seam)"), nil) // D7 rows 2, 6, 18
		return
	}
	if leave, err := p.joinAndPublish(); err != nil { // E8, E9, commit rename
		p.rollback(w, err, &leave)
		return
	}
	p.afterCommit(w)
}

func refusalField(r *requestRefusalError) string {
	if r == nil {
		return ""
	}
	return r.field
}

// crashAt stops the apply at step as a crash would: no compensation, the
// staging kept for start-up recovery. Only the crash seam reaches it.
func (p *packageApplyRun) crashAt(w http.ResponseWriter, step string) bool {
	if packageT5.CrashAt != step {
		return false
	}
	if err := p.a.packageRegistry().NeedsCleanup(p.stagingID); err != nil {
		slog.Warn("packages: crash seam: mark staging", "staging_id", p.stagingID, "error", err)
	}
	jsonErr(w, http.StatusInternalServerError, errPackageCrashSeam.Error()+": "+step)
	return true
}

// createPending is E2 + E4: the coverage check and the D10 re-check under
// configMu, then CreatePendingState. config.json is never written (FR-034).
func (p *packageApplyRun) createPending() error {
	c := &p.plan.Create
	err := p.a.withToolPolicyCoverageCheck(
		func(cfg *config.Config) { cfg.Agents.List = append(cfg.Agents.List, c.Agent) },
		func() error {
			if err := p.a.recheckAgentMaxToolIterationsLocked(c); err != nil {
				return err
			}
			agent := c.Agent
			if _, err := agentstore.New(p.a.homePath).CreatePendingState(agent.ID, &agent, c.Soul, p.plan.AgentMD); err != nil {
				return fmt.Errorf("create pending agent: %w", err)
			}
			return nil
		})
	if err != nil {
		return err
	}
	switch packageT5.CreatePendingFault {
	case "soul":
		return errors.New("create pending agent: SOUL.md replace failed (fault seam)")
	case "agentmd":
		return errors.New("create pending agent: AGENT.md replace failed (fault seam)")
	}
	return nil
}

// joinAndPublish is the one lock hold of mechanism step 2: join W, seed the
// self-row, sync the workspace and delegation directories, then publish (the
// commit point). On failure the helper has already run C1 under the same hold.
func (p *packageApplyRun) joinAndPublish() (systools.LeaveResult, error) {
	cfg := p.a.agentLoop.GetConfig()
	opts := systools.WorkspaceMembershipOptions{
		Home:                 p.a.homePath,
		SelfEdgeExcluded:     cfg.SelfEdgeExcludeAgentIDSet,
		DepthCeiling:         func() int { return delegationDepthCeiling(cfg) },
		SaveDelegation:       packageT5.SaveDelegation,
		WriteWorkspace:       packageT5.WriteWorkspace,
		AfterMembershipWrite: packageT5.AfterMembershipWrite,
	}
	id := p.agentID()
	return systools.JoinWorkspaceTeamAndPublish(opts, p.workspaceID, id, func() error {
		delegationPath, err := workspace.DelegationStorePath(p.a.homePath, p.workspaceID)
		if err != nil {
			return err
		}
		for _, dir := range []string{filepath.Join(p.a.homePath, "workspaces"), filepath.Dir(delegationPath)} {
			if syncErr := syncPackageDir(dir); syncErr != nil {
				return fmt.Errorf("sync before publish: %w", syncErr)
			}
		}
		store := agentstore.New(p.a.homePath)
		if packageT5.PublishRename != nil {
			from := filepath.Join(store.Dir(), id+".pending")
			if renameErr := packageT5.PublishRename(from, filepath.Join(store.Dir(), id+".json")); renameErr != nil {
				return fmt.Errorf("publish pending agent: rename: %w", renameErr)
			}
		}
		err = store.PublishPending(id)
		if errors.Is(err, agentstore.ErrPublishNotDurable) {
			// Chief ruling 1: the rename happened, so the agent is committed and
			// visible. Never roll it back; report it as a warning.
			p.notDurable = true
			slog.Warn("packages: imported agent published, but the directory sync failed", "agent_id", id, "error", err)
			return nil
		}
		return err
	})
}

// discardPending is C2 (or the DiscardPending seam).
func (p *packageApplyRun) discardPending() error {
	if packageT5.DiscardPending != nil {
		return packageT5.DiscardPending(p.agentID())
	}
	return agentstore.New(p.a.homePath).DiscardPending(p.agentID())
}

// rollback answers a failure before the commit point. C1 already ran inside
// the helper (leave is its outcome, nil when the join was not reached); C2
// always runs here (revision 7).
func (p *packageApplyRun) rollback(w http.ResponseWriter, cause error, leave *systools.LeaveResult) {
	slog.Error("packages: import failed before the commit point; rolling back",
		"staging_id", p.stagingID, "agent_id", p.agentID(), "error", cause)
	plan := omnpkg.RollbackPlan{DiscardPendingRecord: p.discardPending}
	if leave != nil {
		plan.MembershipAttempted = leave.MembershipRemoved || leave.FailedStage != ""
		c1Err := fmt.Errorf("leave failed at %s", leave.FailedStage)
		switch leave.FailedStage {
		case systools.LeaveStageReadWorkspace, systools.LeaveStageWriteMembership:
			plan.RemoveMembership = func() error { return c1Err }
		case systools.LeaveStageReadDelegation, systools.LeaveStageSaveDelegation:
			plan.RemoveEdges = func() error { return c1Err }
		}
	}
	res := omnpkg.RunRollback(plan)
	reg := p.a.packageRegistry()
	fields := map[string]string{"outcome": "rolled_back", "staging_id": p.stagingID, "workspace_id": p.workspaceID}
	if !res.Complete {
		fields["outcome"], fields["failed_step"] = "rollback_incomplete", res.FailedStep
	}
	if err := p.a.packageAudit("apply", p.actor, p.agentID(), fields); err != nil {
		slog.Error("packages: audit of a rolled-back import failed", "agent_id", p.agentID(), "error", err)
	}
	if res.Complete {
		if err := p.journal(omnpkg.JournalRolledBack); err != nil {
			slog.Warn("packages: write rolled_back journal", "staging_id", p.stagingID, "error", err)
		}
		if err := reg.FinishApply(p.stagingID); err != nil {
			slog.Warn("packages: retire rolled-back staging", "staging_id", p.stagingID, "error", err)
		}
		p.a.forgetProposedName(p.stagingID)
		writePackageError(w, packageErr(omnpkg.CodeImportRolledBack), nil)
		return
	}
	slog.Error("packages: import rollback incomplete", "staging_id", p.stagingID, "agent_id", p.agentID(),
		"failed_step", res.FailedStep, "leftovers", res.Leftovers, "errors", errors.Join(res.Errors...))
	if err := p.journal(omnpkg.JournalRollbackIncomplete); err != nil {
		slog.Warn("packages: write rollback_incomplete journal", "staging_id", p.stagingID, "error", err)
	}
	if err := reg.NeedsCleanup(p.stagingID); err != nil {
		slog.Warn("packages: mark staging needs-cleanup", "staging_id", p.stagingID, "error", err)
	}
	writePackageError(w, packageErr(omnpkg.CodeImportRollbackIncomplete), map[string]any{
		"failed_step": res.FailedStep,
		"leftovers":   res.Leftovers,
		"agent_id":    p.agentID(),
		"staging_id":  p.stagingID,
	})
}
