//go:build goolm && stdjson

package agent

// Native binding of the D7 row 16 conversion seam (#1963, Part L). It lives in
// its own file because it names a production package (pkg/skillperm) that only
// a tree carrying the Skills & Tools backend has: a tree without it does not
// compile this file, so a tree without the backend must not carry it. The seam
// itself stays in tool_surface_conversion_toolsearch_red_test.go and fails loud
// (BLOCKED) while it is nil.
//
// tests/helpers/nativebinding accepts only a direct, named production function
// of the module, so this cannot be satisfied by a test-side stand-in.

import "github.com/elicify-ai/omnipus/pkg/skillperm"

func init() {
	toolLoadingConversionRunOf = skillperm.Run
}
