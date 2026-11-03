// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// RetiredToolNames lists builtin tool names that were retired and must not
// come back in any selectable list, stored policy or seed (skills spec r15
// §10: create_skill → write_file of SKILL.md, remove_skill → edit_skill
// action=delete). It is the prune input T8's field selection and policy prune
// read; a call to one of these names is answered from
// retiredToolCanonicalNames, never executed.
var RetiredToolNames = []string{
	"create_skill",
	"remove_skill",
}
