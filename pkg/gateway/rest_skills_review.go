// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Skills & Tools C-13: the install review. POST /skills/install/review stages
// a skill without publishing it and pins its revision; POST /skills/install
// with that review_id publishes exactly the staged folder, once. The review
// id is also the install operation's id: GET /skills/install/review/{id}
// tells what really happened, DELETE cancels a staged review. A staged
// review lives 15 minutes; a finished one is kept 15 minutes, then removed.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// installReviewLife is how long a staged review waits for Install, and how
// long a finished review's record is kept (C-13).
const installReviewLife = 15 * time.Minute

// Install review answers (C-13).
const (
	reviewStaleText     = "The skill changed after review. Review it again."
	reviewExpiredText   = "This review expired. Open the skill again to review it."
	reviewCancelledText = "This review was cancelled. Nothing was installed."
	reviewUnknownText   = "This review does not exist. Open the skill again to review it."
)

// installReview is one review and its install operation.
type installReview struct {
	id           string
	slug         string
	stageDir     string
	revision     string
	version      string
	verification gen.SkillInstallReviewVerification
	result       *skills.InstallResult
	state        gen.SkillInstallReviewStatusState
	expiresAt    time.Time
	endedAt      time.Time
	reason       string
	installed    *gen.Skill
	replaced     *string
}

// installReviews holds the reviews of one gateway. The zero value is ready.
type installReviews struct {
	mu   sync.Mutex
	now  func() time.Time
	byID map[string]*installReview
}

func (s *installReviews) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// sweepLocked expires staged reviews whose time is up (their staged copy is
// removed) and forgets finished reviews kept for their full time.
func (s *installReviews) sweepLocked(now time.Time) {
	for id, rv := range s.byID {
		if rv.state == gen.SkillInstallReviewStatusStateStaged && !now.Before(rv.expiresAt) {
			rv.state = gen.SkillInstallReviewStatusStateExpired
			rv.endedAt = rv.expiresAt
			discardReviewStage(rv)
		}
		if isTerminalReview(rv.state) && !now.Before(rv.endedAt.Add(installReviewLife)) {
			delete(s.byID, id)
		}
	}
}

// finishLocked moves a review to a terminal state exactly once.
func (s *installReviews) finishLocked(rv *installReview, state gen.SkillInstallReviewStatusState, reason string) {
	rv.state = state
	rv.reason = reason
	rv.endedAt = s.clock()
	discardReviewStage(rv)
}

func isTerminalReview(state gen.SkillInstallReviewStatusState) bool {
	return state != gen.SkillInstallReviewStatusStateStaged && state != gen.SkillInstallReviewStatusStatePublishing
}

// discardReviewStage removes the review's staged copy. A failed removal is
// logged: the staging sweep of the next install removes it later.
func discardReviewStage(rv *installReview) {
	if rv.stageDir == "" {
		return
	}
	if err := os.RemoveAll(rv.stageDir); err != nil {
		logsafeWarn("rest: could not remove the staged copy of an install review",
			"review_id", rv.id, "error", err)
		return
	}
	rv.stageDir = ""
}

// setInstallReviewClock sets the clock the install reviews read (stage,
// status, submit and retention) and returns the function that restores the
// previous one.
func (a *restAPI) setInstallReviewClock(now func() time.Time) func() {
	a.installReviews.mu.Lock()
	defer a.installReviews.mu.Unlock()
	previous := a.installReviews.now
	a.installReviews.now = now
	return func() {
		a.installReviews.mu.Lock()
		defer a.installReviews.mu.Unlock()
		a.installReviews.now = previous
	}
}

func newReviewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("review id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// reviewVerification maps the staging result onto the C-13 vocabulary. A
// mismatching archive never reaches here: the registry refuses it.
func reviewVerification(result *skills.InstallResult) gen.SkillInstallReviewVerification {
	if result != nil && result.Verified {
		return gen.SkillInstallReviewVerificationMatched
	}
	return gen.SkillInstallReviewVerificationNoRegistryHash
}

// stageInstallReview records a staged skill as a review and answers it. The
// staged copy now belongs to the review.
func (a *restAPI) stageInstallReview(w http.ResponseWriter, slug, skillsRoot, stageDir string, result *skills.InstallResult) {
	revision, err := skills.ContentRevision(stageDir)
	if err != nil {
		_ = os.RemoveAll(stageDir)
		jsonServerFailure(w, http.StatusInternalServerError, "could not read the staged skill", err)
		return
	}
	id, err := newReviewID()
	if err != nil {
		_ = os.RemoveAll(stageDir)
		jsonServerFailure(w, http.StatusInternalServerError, "could not start the review", err)
		return
	}
	replaces := false
	hasLocal := false
	if _, statErr := os.Stat(filepath.Join(skillsRoot, slug, "SKILL.md")); statErr == nil {
		replaces = true
		if hasLocal, err = readListedSkillLocalChanges(skillsRoot, slug); err != nil {
			_ = os.RemoveAll(stageDir)
			jsonServerFailure(w, http.StatusInternalServerError, "could not read the installed skill", err)
			return
		}
	}
	rv := &installReview{
		id: id, slug: slug, stageDir: stageDir, revision: revision,
		version: installedSkillVersion(result), verification: reviewVerification(result),
		result: result, state: gen.SkillInstallReviewStatusStateStaged,
	}
	s := &a.installReviews
	s.mu.Lock()
	now := s.clock()
	s.sweepLocked(now)
	rv.expiresAt = now.Add(installReviewLife)
	if s.byID == nil {
		s.byID = map[string]*installReview{}
	}
	s.byID[id] = rv
	s.mu.Unlock()

	review := gen.SkillInstallReview{
		ReviewId: id, Name: slug, Version: rv.version, Revision: revision,
		Verification: rv.verification, Verified: rv.verification == gen.SkillInstallReviewVerificationMatched,
		ReplacesExisting: replaces, HasLocalChangesToReplace: hasLocal, ExpiresAt: rv.expiresAt,
	}
	if c := skills.FileCategory(stageDir); c != "" {
		suggested := gen.SkillInstallReviewSuggestedCategory(c)
		review.SuggestedCategory = &suggested
	}
	jsonOK(w, review)
}

// reviewStatus is the C-13 status of a review.
func reviewStatus(rv *installReview) gen.SkillInstallReviewStatus {
	out := gen.SkillInstallReviewStatus{ReviewId: rv.id, State: rv.state, Revision: rv.revision}
	if rv.state == gen.SkillInstallReviewStatusStateInstalled && rv.installed != nil {
		installed := rv.installed.Revision
		out.InstalledRevision = &installed
		out.ReplacedRevision = rv.replaced
	}
	if rv.state == gen.SkillInstallReviewStatusStateFailed {
		reason := rv.reason
		out.Reason = &reason
	}
	return out
}

// handleInstallReview serves GET and DELETE /skills/install/review/{id}.
func (a *restAPI) handleInstallReview(w http.ResponseWriter, r *http.Request, id string) {
	s := &a.installReviews
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	rv, ok := s.byID[id]
	if !ok {
		jsonErr(w, http.StatusNotFound, reviewUnknownText)
		return
	}
	switch r.Method {
	case http.MethodGet:
		jsonOK(w, reviewStatus(rv))
	case http.MethodDelete:
		if rv.state != gen.SkillInstallReviewStatusStateStaged {
			// Only a staged review can be cancelled; a finished or
			// publishing one keeps its outcome.
			jsonErr(w, http.StatusNotFound, reviewUnknownText)
			return
		}
		s.finishLocked(rv, gen.SkillInstallReviewStatusStateCancelled, "")
		w.WriteHeader(http.StatusNoContent)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// claimReview takes a staged review for publication, or answers the
// repeated or refused submit itself. ok is true when the caller publishes.
func (a *restAPI) claimReview(w http.ResponseWriter, id string) (*installReview, bool) {
	s := &a.installReviews
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	rv, ok := s.byID[id]
	if !ok {
		jsonErr(w, http.StatusNotFound, reviewUnknownText)
		return nil, false
	}
	switch rv.state {
	case gen.SkillInstallReviewStatusStatePublishing:
		writeJSON(w, http.StatusAccepted, reviewStatus(rv))
		return nil, false
	case gen.SkillInstallReviewStatusStateInstalled:
		jsonOK(w, *rv.installed)
		return nil, false
	case gen.SkillInstallReviewStatusStateFailed:
		jsonErr(w, http.StatusConflict, "Not installed: "+strings.TrimSuffix(rv.reason, ".")+".")
		return nil, false
	case gen.SkillInstallReviewStatusStateExpired:
		jsonErr(w, http.StatusGone, reviewExpiredText)
		return nil, false
	case gen.SkillInstallReviewStatusStateCancelled:
		jsonErr(w, http.StatusConflict, reviewCancelledText)
		return nil, false
	}
	current, err := skills.ContentRevision(rv.stageDir)
	if err != nil || current != rv.revision {
		s.finishLocked(rv, gen.SkillInstallReviewStatusStateFailed, reviewStaleText)
		jsonErr(w, http.StatusConflict, reviewStaleText)
		return nil, false
	}
	if refusal := a.unverifiedInstallRefusal(rv.result, true); refusal != nil {
		s.finishLocked(rv, gen.SkillInstallReviewStatusStateFailed, refusal.text)
		jsonErr(w, refusal.status, refusal.text)
		return nil, false
	}
	rv.state = gen.SkillInstallReviewStatusStatePublishing
	return rv, true
}

// submitReviewedInstall publishes exactly the staged folder of a review
// (C-13) and records the person's trust approval of that revision (C-12
// row 9).
func (a *restAPI) submitReviewedInstall(w http.ResponseWriter, r *http.Request, req gen.SkillInstallRequest) {
	rv, ok := a.claimReview(w, strings.TrimSpace(*req.ReviewId))
	if !ok {
		return
	}
	skillsRoot := filepath.Join(a.homePath, "skills")
	var replaced *string
	if before, err := skills.ContentRevision(filepath.Join(skillsRoot, rv.slug)); err == nil {
		replaced = &before
	}
	skill, publish, err := a.publishStagedSkill(req, rv.slug, skillsRoot, rv.stageDir, rv.result)
	if err == nil && !skill.Verified {
		// The skill is installed either way; a lost approval is said on
		// the result, and the trust check asks again at its first load.
		if recErr := a.recordReviewTrustApproval(r, skillsRoot, rv.slug, skill.Revision); recErr != nil {
			logsafeWarn("rest: reviewed skill installed but its trust approval was not saved", "slug", rv.slug, "error", recErr)
			msg := "The skill was installed, but its review approval was not saved; it asks for approval at its first use."
			if skill.Message != nil {
				msg = *skill.Message + " " + msg
			}
			skill.Message = &msg
		} else {
			skill.TrustApproved = true
		}
	}

	s := &a.installReviews
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		reason := publish.Message
		if reason == "" {
			reason = err.Error()
		}
		s.finishLocked(rv, gen.SkillInstallReviewStatusStateFailed, reason)
		writeSkillChangeFailure(w, rv.slug, publish, err)
		return
	}
	rv.installed = &skill
	rv.replaced = replaced
	s.finishLocked(rv, gen.SkillInstallReviewStatusStateInstalled, "")
	jsonOK(w, skill)
}

// recordReviewTrustApproval stores the person's trust approval of the
// reviewed revision of an unverified skill.
func (a *restAPI) recordReviewTrustApproval(r *http.Request, skillsRoot, slug, revision string) error {
	_, err := a.recordTrustApproval(r, skillsRoot, slug, revision)
	return err
}
