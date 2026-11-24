// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Card answers and their refusals (Skills & Tools C-24, C-26).
const (
	approvalRetiredAllowText   = "This choice is no longer offered. Choose Approve Once or a time limit."
	approvalDurationChoiceText = "Choose 1 hour, 3 hours or 1 day."
	approvalDurationOnlyText   = "A time limit applies only to Allow for."
	approvalNotOfferedText     = "This choice is not available for this request."
	approvalNotAppliedPrefix   = "Approved for this request only. The time limit was not saved: "
)

// approvalAllowDurations are the time choices of every card that offers a
// *_allow_for action (C-26).
var approvalAllowDurations = []string{
	string(gen.ApprovalAllowDurationN1h),
	string(gen.ApprovalAllowDurationN3h),
	string(gen.ApprovalAllowDurationN1d),
}

// oneRequestKinds are the shell escalations whose answer covers this one
// call only: the blind-spot file check records nothing to widen (C-26).
var oneRequestKinds = map[string]bool{"fs_preflight_blind": true}

// shellPreflightKinds are the shell pre-flight cards (D7, D8) whose narrow
// allowance (one path with its access, the named addresses) the shell tool
// records itself once the call is released.
var shellPreflightKinds = map[string]bool{"fs_preflight": true, "network_preflight": true}

func approvalKind(e *approvalEntry) string {
	kind, _ := e.Args["adr092_kind"].(string)
	return kind
}

// offeredActions computes the answers a card offers and the time choices
// that go with them, when the card is raised (C-24, C-26). The server
// accepts only these. An always-ask tool, an exact-action tool and a
// one-request shell escalation offer no time choice; every other tool card
// offers Allow for 1 hour / 3 hours / 1 day. Cancel is on every card, as
// today.
func offeredActions(e *approvalEntry) (actions, durations []string) {
	once := []string{
		string(gen.ToolApprovalActionRequestActionAllowOnce),
		string(gen.ToolApprovalActionRequestActionDeny),
		string(gen.ToolApprovalActionRequestActionCancel),
	}
	if tools.IsAlwaysAsk(e.ToolName) || tools.IsExactActionTool(e.ToolName) || oneRequestKinds[approvalKind(e)] {
		return once, []string{}
	}
	return []string{
		string(gen.ToolApprovalActionRequestActionAllowOnce),
		string(gen.ToolApprovalActionRequestActionAllowFor),
		string(gen.ToolApprovalActionRequestActionDeny),
		string(gen.ToolApprovalActionRequestActionCancel),
	}, slices.Clone(approvalAllowDurations)
}

// isOffered reports whether action is one of the card's offered answers.
func isOffered(e *approvalEntry, action gen.ToolApprovalActionRequestAction) bool {
	actions, _ := offeredActions(e)
	return slices.Contains(actions, string(action))
}

// approvalAnswer is a parsed card answer: the action and, for a time
// choice, its length.
type approvalAnswer struct {
	body     gen.ToolApprovalActionRequest
	allowFor time.Duration
}

// readApprovalAnswer decodes and checks a card answer. It writes the
// refusal and returns false when the answer is malformed (C-26 refusals),
// before any card is looked up, so the card stays as it was.
func (a *restAPI) readApprovalAnswer(w http.ResponseWriter, r *http.Request) (approvalAnswer, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return approvalAnswer{}, false
	}
	// A screen from before the upgrade still sends "allow": name the new
	// choices instead of a schema error.
	var probe struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(raw, &probe) == nil && probe.Action == "allow" {
		jsonErr(w, http.StatusBadRequest, approvalRetiredAllowText)
		return approvalAnswer{}, false
	}
	var ans approvalAnswer
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if !decodeAndValidate(w, r, "ToolApprovalActionRequest", &ans.body, validateEnabled) {
		return approvalAnswer{}, false
	}
	timed := ans.body.Action == gen.ToolApprovalActionRequestActionAllowFor ||
		ans.body.Action == gen.ToolApprovalActionRequestActionApproveVersionAllowFor
	switch {
	case timed:
		if ans.body.Duration == nil {
			jsonErr(w, http.StatusBadRequest, approvalDurationChoiceText)
			return approvalAnswer{}, false
		}
		d, ok := allowDurationOf(*ans.body.Duration)
		if !ok {
			jsonErr(w, http.StatusBadRequest, approvalDurationChoiceText)
			return approvalAnswer{}, false
		}
		ans.allowFor = d
	case ans.body.Duration != nil:
		jsonErr(w, http.StatusBadRequest, approvalDurationOnlyText)
		return approvalAnswer{}, false
	}
	return ans, true
}

// allowDurationOf maps the wire time choice to its length.
func allowDurationOf(d gen.ApprovalAllowDuration) (time.Duration, bool) {
	switch d {
	case gen.ApprovalAllowDurationN1h:
		return time.Hour, true
	case gen.ApprovalAllowDurationN3h:
		return 3 * time.Hour, true
	case gen.ApprovalAllowDurationN1d:
		return 24 * time.Hour, true
	}
	return 0, false
}

// errAllowanceNotRecorded is the reason a time choice was not stored.
var errAllowanceNotRecorded = errors.New("the request has no agent or tool to allow")

// recordTimedAllowance stores the card's timed allowance for the agent the
// card names (C-26), never for the agent of the chat it is shown in. An
// ordinary tool is covered whole; a bash call by its exact command or, with
// scope prefix, its command family. A shell pre-flight card's narrow
// allowance is recorded by the shell tool itself when the call is released,
// so nothing is stored here for it and no end time is known.
func (a *restAPI) recordTimedAllowance(
	entry *approvalEntry, requested gen.ToolApprovalActionRequestScope, d time.Duration,
) (time.Time, gen.ToolApprovalResponseScope, error) {
	if shellPreflightKinds[approvalKind(entry)] {
		return time.Time{}, gen.ToolApprovalResponseScopeExact, nil
	}
	grants := a.agentLoop.ApprovalGrants()
	if requested == gen.ToolApprovalActionRequestScopePrefix && entry.ToolName == "bash" {
		if g, ok := tools.BashPrefixGrantFor(entry.Args); ok {
			end, ok := grants.RecordPrefixGrantFor(entry.AgentID, entry.ToolName, g, d)
			if !ok {
				return time.Time{}, gen.ToolApprovalResponseScopePrefix, errAllowanceNotRecorded
			}
			return end, gen.ToolApprovalResponseScopePrefix, nil
		}
	}
	end, ok := grants.RecordFor(entry.AgentID, entry.ToolName, entry.Args, d)
	if !ok {
		return time.Time{}, gen.ToolApprovalResponseScopeExact, errAllowanceNotRecorded
	}
	return end, gen.ToolApprovalResponseScopeExact, nil
}
