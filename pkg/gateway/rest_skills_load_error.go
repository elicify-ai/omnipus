// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"io/fs"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
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
