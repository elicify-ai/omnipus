// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"io/fs"
	"path/filepath"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// unloadableSkillRevision stands in for the revision of a skill whose
// files could not be read (FR-063): the SHA-256 of nothing, which matches
// no stored state, so a write naming it is refused as stale.
const unloadableSkillRevision = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// markSkillUnloadable sets status error and the plain-words reason (no
// paths) on a listed skill whose files could not be read (FR-063, S-80).
func markSkillUnloadable(skill *gen.Skill, err error) {
	reason := "the skill folder could not be read"
	switch {
	case errors.Is(err, fs.ErrPermission):
		reason = "a file in the skill folder is not readable (permission denied)"
	case errors.Is(err, fs.ErrNotExist):
		reason = "a file in the skill folder is missing"
	}
	skill.Status = gen.SkillStatusError
	skill.LoadError = &reason
	skill.Revision = unloadableSkillRevision
	skill.HasLocalChanges = false
}

// fillSkillRecords sets the C-5 category and verification and the C-24
// trust approval of a listed skill from Omnipus data. A built-in takes its
// category from its own file and is always verified. A record that cannot
// be read leaves the skill uncategorised and unverified, and says so on the
// skill.
func (a *restAPI) fillSkillRecords(skill *gen.Skill, root string, builtin bool) {
	skill.Category = gen.SkillCategory(skills.CategoryUncategorised)
	if builtin {
		if c := skills.FileCategory(filepath.Join(root, skill.Id)); c != "" {
			skill.Category = gen.SkillCategory(c)
		}
		skill.Verified = true
		return
	}
	category, err := skills.SkillCategoryOf(root, skill.Id)
	if err == nil {
		skill.Category = gen.SkillCategory(category)
		skill.Verified, _, err = skills.SkillVerified(root, skill.Id)
	}
	if err == nil {
		skill.TrustApproved, err = skills.TrustApproved(root, skills.SkillKey(skill.Id), skill.Revision)
	}
	if err != nil {
		logsafeWarn("rest: read skill install records", "skill", skill.Id, "error", err)
		skill.Verified, skill.TrustApproved = false, false
		msg := "The category, verification and approval of this skill could not be read: " + err.Error()
		skill.Message = &msg
	}
}
