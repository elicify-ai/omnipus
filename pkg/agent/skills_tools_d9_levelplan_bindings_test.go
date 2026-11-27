//go:build goolm && stdjson

package agent

// Native binding of the C-15 level-authority seam (#1963, Part L, Ruling A). It
// lives in its own file because it names a production symbol
// (tools.ToolSurface.Plan) that only a tree carrying the Skills & Tools backend
// has: a tree without it does not compile this file, so a tree without the
// backend must not carry it. The seam stays in
// skill_visibility_manifest_red_test.go, typed `any` and called by field name
// through tests/helpers/structcall, and fails loud (BLOCKED) while it is nil.
//
// File order matters. skills_tools_d9_bindings_test.go (the backend's) assigns
// the same seam from the test-only wrapper toolLevelTurnPlan; Go runs init
// functions in file-name order, and this file's name sorts after that one, so
// this assignment is the one that holds until the backend deletes its wrapper
// and its line. tests/helpers/nativebinding accepts only a direct, named
// production function or method expression of the module (here pkg/tools), so a
// test-side stand-in cannot satisfy it.

import "github.com/elicify-ai/omnipus/pkg/tools"

func init() {
	levelTurnPlanOf = tools.ToolSurface.Plan
}
