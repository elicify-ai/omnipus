//go:build goolm && stdjson

package tools

// Native bindings of the folder-authority allowance seams (#1963, Part L).
// They live in their own file because they name production symbols
// ((*security.ApprovalGrantStore).RecordSkillAllowance and
// SetSkillAllowanceStore) that only a tree carrying the Skills & Tools backend
// has: a tree without them does not compile this file, so a tree without the
// backend must not carry it. The seams stay in
// skill_authority_folders_follow_outcome_red_test.go, typed exactly as these
// production functions, and fail loud (BLOCKED) while they are nil.
//
// tests/helpers/nativebinding accepts only a direct, named production function
// or method expression of the module, so a test-side stand-in cannot satisfy it.

import "github.com/elicify-ai/omnipus/pkg/security"

func init() {
	recordSkillAllowanceOf = (*security.ApprovalGrantStore).RecordSkillAllowance
	registerSkillAllowanceStoreOf = SetSkillAllowanceStore
}
