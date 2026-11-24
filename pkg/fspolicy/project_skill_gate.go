// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

// Skills & Tools C-25: the project-skill gate. A project skill folder under a
// mount (<mount>/.omnipus/skills/<slug>, <mount>/.claude/skills/<slug>) is an
// ordinary mount file to every other rule, so filtering the installed
// loadable set does not stop it being read. The turn resolver lists every
// reachable project skill folder whose outcome (C-11) for the acting agent
// is not "usable now"; IsCarveOut refuses those folders ahead of every
// mount, work dir, path grant and God Mode widening, so a broader grant
// never re-opens one. An editor (SkillsReadAll) may read them for editing,
// never run a helper from them (ProjectSkillRunGate).

import "path/filepath"

// Project skill gate outcomes.
const (
	ProjectSkillGateDeny = "deny"
	ProjectSkillGateAsk  = "ask_pending"
)

// ProjectSkillGate is one gated project skill folder of the turn.
type ProjectSkillGate struct {
	// Folder is the real path of the skill folder.
	Folder string
	// Slug is the skill name the refusal names.
	Slug string
	// Outcome is ProjectSkillGateDeny or ProjectSkillGateAsk.
	Outcome string
	// Reason is the refusal text for the person and the model.
	Reason string
}

// ProjectSkillGateFor returns the gate covering cleanPath, if any.
// Identity-based containment, so case, Unicode and symlink spellings of one
// folder get one answer.
func ProjectSkillGateFor(cleanPath string, policy FSPolicy) (ProjectSkillGate, bool) {
	for _, g := range policy.HomeGuard.ProjectSkillGates {
		if g.Folder != "" && CoversForDeny(filepath.Clean(g.Folder), cleanPath) {
			return g, true
		}
	}
	return ProjectSkillGate{}, false
}

// ProjectSkillReadGate returns the gate refusing a read of cleanPath: every
// gate, except for an editor (SkillsReadAll), who reads for editing.
func ProjectSkillReadGate(cleanPath string, policy FSPolicy) (ProjectSkillGate, bool) {
	if policy.HomeGuard.SkillsReadAll {
		return ProjectSkillGate{}, false
	}
	return ProjectSkillGateFor(filepath.Clean(cleanPath), policy)
}

// ProjectSkillRunGate returns the gate refusing a helper run from cleanPath:
// every gate, the editor included (C-25 editor exception: read only).
func ProjectSkillRunGate(cleanPath string, policy FSPolicy) (ProjectSkillGate, bool) {
	return ProjectSkillGateFor(filepath.Clean(cleanPath), policy)
}
