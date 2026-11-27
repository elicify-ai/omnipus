//go:build goolm && stdjson

package tools

// Native binding of the S-71 category reader (#1963, Part L). It lives in its
// own file because it names a production symbol (pkg/skills.SkillCategoryOf)
// that only a tree carrying the Skills & Tools backend has: a tree without it
// does not compile this file, so a tree without the backend must not carry it.
// Every test file keeps the seam itself, typed to the production signature,
// and fails loud (BLOCKED) while the seam is nil.
//
// tests/helpers/nativebinding accepts only a direct, named production
// function of the module, so this cannot be satisfied by a test-side stand-in.

import "github.com/elicify-ai/omnipus/pkg/skills"

func init() {
	readInstalledSkillCategory = skills.SkillCategoryOf
}
