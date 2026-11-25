// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Skill approval cards (Skills & Tools C-9, C-12, C-24, C-26). A Skill
// load or install_skill card names its skill; which checks are open
// decides its answers. Approving one re-checks the skill's facts, then
// writes the trust approval, then records the timed allowance, then
// releases the waiting call.

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

const (
	approvalSaveFailedText = "Could not save the approval. Nothing was changed."
	skillCheckPermission   = "permission"
	skillCheckTrust        = "trust"
)

// skillAnswerMu serialises skill card answers with their trust-approval
// writes, so the re-check and the effects see one state (C-24).
var skillAnswerMu sync.Mutex

// errSkillAllowanceNotRecorded is the reason a skill allowance was not stored.
var errSkillAllowanceNotRecorded = errors.New("the request has no agent or skill to allow")

// approvalSkill projects the card's skill (C-9); nil on every other card.
func approvalSkill(e *approvalEntry) *gen.ToolApprovalSkill {
	if e == nil || e.Skill == nil {
		return nil
	}
	s := *e.Skill
	s.OpenChecks = slices.Clone(e.Skill.OpenChecks)
	if s.OpenChecks == nil {
		s.OpenChecks = []string{}
	}
	return &s
}

func skillCheckOpen(e *approvalEntry, check string) bool {
	return e.Skill != nil && slices.Contains(e.Skill.OpenChecks, check)
}

// skillCardOffers is the C-12/C-26 answer set of a skill card; ok is false
// for every other card. A Skill load: the permission check alone offers
// Approve Once, Allow for and Deny; the trust check alone Approve this
// version and Deny; both, one combined card. An install whose staged copy
// needs trust: with the permission check, Approve Once and Deny (approving
// also approves the staged version); alone, Approve this version and Deny.
func skillCardOffers(e *approvalEntry) (actions, durations []string, ok bool) {
	trust := skillCheckOpen(e, skillCheckTrust)
	switch e.ToolName {
	case "Skill":
		permission := e.Skill == nil || skillCheckOpen(e, skillCheckPermission)
		switch {
		case trust && permission:
			return []string{
				string(gen.ToolApprovalActionRequestActionApproveVersion),
				string(gen.ToolApprovalActionRequestActionApproveVersionAllowFor),
				string(gen.ToolApprovalActionRequestActionDeny),
			}, slices.Clone(approvalAllowDurations), true
		case trust:
			return trustCardActions(), []string{}, true
		default:
			return []string{
				string(gen.ToolApprovalActionRequestActionAllowOnce),
				string(gen.ToolApprovalActionRequestActionAllowFor),
				string(gen.ToolApprovalActionRequestActionDeny),
			}, slices.Clone(approvalAllowDurations), true
		}
	case "install_skill":
		switch {
		case trust && skillCheckOpen(e, skillCheckPermission):
			return []string{
				string(gen.ToolApprovalActionRequestActionAllowOnce),
				string(gen.ToolApprovalActionRequestActionDeny),
			}, []string{}, true
		case trust:
			return trustCardActions(), []string{}, true
		}
	}
	return nil, nil, false
}

func trustCardActions() []string {
	return []string{
		string(gen.ToolApprovalActionRequestActionApproveVersion),
		string(gen.ToolApprovalActionRequestActionDeny),
	}
}

// answerSkillCard applies an approving answer to a skill card in the C-24
// order: re-check (409 and the waiting call refused when the facts
// changed), the trust approval (500 and the card still open when it
// fails), the timed allowance (a failure still releases this call), then
// the release.
func (a *restAPI) answerSkillCard(w http.ResponseWriter, r *http.Request, entry *approvalEntry, ans approvalAnswer) {
	skillAnswerMu.Lock()
	defer skillAnswerMu.Unlock()
	id := entry.ApprovalID
	if !a.approvalReg.isPending(id) {
		jsonErr(w, http.StatusGone, "approval already resolved")
		return
	}
	if entry.skillRecheck != nil {
		if reason := entry.skillRecheck(); reason != "" {
			a.approvalReg.refuseStale(id, reason)
			jsonErr(w, http.StatusConflict, reason)
			return
		}
	}
	hooks := a.agentLoop.SkillEffectHooks()
	resp := gen.ToolApprovalResponse{
		ApprovalId: id,
		Action:     gen.ToolApprovalResponseAction(ans.body.Action),
		Status:     gen.ToolApprovalResponseStatusOk,
		Applied:    &gen.ToolApprovalApplied{},
	}
	if skillCheckOpen(entry, skillCheckTrust) {
		if err := a.writeCardTrust(r, entry, hooks.BeforeTrustWrite); err != nil {
			logsafeError("tool-approval: the skill's trust approval was not saved; the card stays open",
				"approval_id", id, "skill", entry.Skill.Key, "error", err)
			jsonErr(w, http.StatusInternalServerError, approvalSaveFailedText)
			return
		}
		resp.Applied.TrustApproved = true
	}
	var allowance *timedAllowanceAnswer
	if ans.allowFor > 0 {
		allowance = a.recordSkillAllowance(entry, ans, hooks.BeforeAllowanceRecord, &resp)
	}
	if hooks.BeforeWaitingRelease != nil {
		hooks.BeforeWaitingRelease()
	}
	resolved, gone := a.approvalReg.resolveAnswer(id, ApprovalActionApprove, false, requestUsername(r), allowance, ans.allowFor)
	if gone {
		jsonErr(w, http.StatusGone, "approval already resolved")
		return
	}
	if !resolved {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("approval %q not found", id))
		return
	}
	a.approvalReg.forget(id) // a replay finds nothing (C-24)
	resp.Applied.CallReleased = true
	jsonOK(w, resp)
}

// writeCardTrust records the person's approval of the card's revision.
func (a *restAPI) writeCardTrust(r *http.Request, entry *approvalEntry, before func() error) error {
	if before != nil {
		if err := before(); err != nil {
			return err
		}
	}
	_, err := a.recordTrustApproval(r, filepath.Join(a.homePath, "skills"), entry.Skill.Key, entry.Skill.Revision)
	return err
}

// recordSkillAllowance stores the card's timed allowance for its agent and
// skill (C-26) and fills the response with what was stored. A failure is
// reported on the response; the call is still released.
func (a *restAPI) recordSkillAllowance(
	entry *approvalEntry, ans approvalAnswer, before func() error, resp *gen.ToolApprovalResponse,
) *timedAllowanceAnswer {
	var err error
	if before != nil {
		err = before()
	}
	var end time.Time
	if err == nil {
		var ok bool
		end, ok = a.agentLoop.ApprovalGrants().RecordSkillAllowance(entry.AgentID, config.NormalizeSkillKey(entry.Skill.Key), ans.allowFor)
		if !ok {
			err = errSkillAllowanceNotRecorded
		}
	}
	if err != nil {
		logsafeWarn("tool-approval: the skill's time limit was not recorded; this call is approved once",
			"approval_id", entry.ApprovalID, "agent_id", entry.AgentID, "skill", entry.Skill.Key, "error", err)
		resp.GrantRecorded = boolPtr(false)
		reason := approvalNotAppliedPrefix + err.Error() + "."
		resp.NotAppliedReason = &reason
		return nil
	}
	resp.GrantRecorded = boolPtr(true)
	resp.ExpiresAt = &end
	resp.Applied.TimedAllowance = true
	duration := string(*ans.body.Duration)
	logsafeInfo("tool-approval: recorded timed skill allowance",
		"approval_id", entry.ApprovalID, "agent_id", entry.AgentID, "skill", entry.Skill.Key, "duration", duration)
	return &timedAllowanceAnswer{Duration: duration, EndsAt: end, Action: string(ans.body.Action)}
}

// refuseStale ends a pending skill card whose answer the re-check refused
// (C-24): the waiting call gets reason, and the card is gone at once so a
// replay finds nothing.
func (r *approvalRegistryV2) refuseStale(approvalID, reason string) bool {
	r.mu.Lock()
	e, ok := r.entries[approvalID]
	if !ok || e.state.isTerminal() {
		r.mu.Unlock()
		return false
	}
	e.state = ApprovalStateDeniedUser
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
	r.pendingCount.Add(-1)
	delete(r.entries, approvalID)
	listener := r.resolutionListener
	r.mu.Unlock()
	e.resultCh <- ApprovalOutcome{Approved: false, Reason: reason, Stale: true}
	if listener != nil {
		listener(e, ApprovalStateDeniedUser)
	}
	return true
}

// forget removes an answered card at once (C-24: no second effect on
// replay). A pending card is kept.
func (r *approvalRegistryV2) forget(approvalID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[approvalID]; ok && e.state.isTerminal() {
		delete(r.entries, approvalID)
	}
}
