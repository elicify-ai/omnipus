// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Skills & Tools C-24: a person's trust approval of one revision of an
// unverified skill. POST /skills/{name}/trust-approval is More actions →
// "Approve this version"; the install review (C-13) records the same
// approval for the revision it installed. Approvals live in the protected
// system area and each one is audited as skill.trust_approved.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

// Trust approval answers (C-24).
const (
	trustApprovalStaleText    = "This skill changed since the page loaded. Review it again."
	trustApprovalVerifiedText = "This skill is verified; it needs no approval."
	trustApprovalUnknownText  = "This skill is not installed."
)

// reviewTokenApprover names the approver of a request admitted with the
// gateway token, which carries no user name.
const reviewTokenApprover = "operator"

// approveSkillVersion handles POST /skills/{name}/trust-approval.
func (a *restAPI) approveSkillVersion(w http.ResponseWriter, r *http.Request, name string) {
	var req gen.SkillTrustApprovalRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "SkillTrustApprovalRequest", &req, validateEnabled) {
		return
	}
	if utils.ValidateSkillIdentifier(name) != nil {
		jsonErr(w, http.StatusNotFound, trustApprovalUnknownText)
		return
	}
	root := filepath.Join(a.homePath, "skills")
	verified, revision, err := skills.SkillVerified(root, name)
	switch {
	case errors.Is(err, skills.ErrNotFound):
		jsonErr(w, http.StatusNotFound, trustApprovalUnknownText)
		return
	case err != nil:
		jsonServerFailure(w, http.StatusInternalServerError, "could not read the skill", err)
		return
	case verified:
		jsonErr(w, http.StatusBadRequest, trustApprovalVerifiedText)
		return
	case revision != strings.TrimSpace(req.Revision):
		jsonErr(w, http.StatusConflict, trustApprovalStaleText)
		return
	}
	approval, err := a.recordTrustApproval(r, root, name, revision)
	if err != nil {
		jsonServerFailure(w, http.StatusInternalServerError, "could not save the approval", err)
		return
	}
	jsonOK(w, gen.SkillTrustApproval{
		Key: approval.Key, Revision: approval.Revision,
		ApprovedBy: approval.ApprovedBy, ApprovedAt: approval.ApprovedAt,
	})
}

// recordTrustApproval stores and audits a person's approval of revision.
func (a *restAPI) recordTrustApproval(r *http.Request, skillsRoot, slug, revision string) (skills.TrustApproval, error) {
	by := requestUsername(r)
	if by == "" {
		by = reviewTokenApprover
	}
	approval := skills.TrustApproval{
		Key: skills.SkillKey(slug), Revision: revision,
		ApprovedBy: by, ApprovedAt: time.Now().UTC(),
	}
	if err := skills.RecordTrustApproval(skillsRoot, approval); err != nil {
		return approval, fmt.Errorf("record trust approval: %w", err)
	}
	audit.Emit(context.Background(), a.packageAuditLogger(), audit.EventSkillTrustApproved, audit.SeverityInfo,
		map[string]any{"key": approval.Key, "revision": approval.Revision, "actor": approval.ApprovedBy})
	return approval, nil
}
