//go:build goolm && stdjson

package tools

// Native binding of the C-11 per-skill decision seam (#1963, Part L, Ruling A).
// It lives in its own file because it names a production symbol
// (ResolveSkillDecision) that only a tree carrying the Skills & Tools backend
// has: a tree without it does not compile this file, so a tree without the
// backend must not carry it. The seam stays in
// skill_policy_resolution_red_test.go, typed `any` and called by field name
// through tests/helpers/structcall, and fails loud (BLOCKED) while it is nil.
//
// File order matters. skill_policy_bindings_test.go (the backend's) assigns the
// same seam from the test-only wrapper SkillOutcomeOf; Go runs init functions
// in file-name order, and this file's name sorts after that one, so this
// assignment is the one that holds until the backend deletes its wrapper and
// its line. tests/helpers/nativebinding accepts only a direct, named production
// function of the module, so a test-side stand-in cannot satisfy it.

func init() {
	skillOutcomeOf = ResolveSkillDecision
}
