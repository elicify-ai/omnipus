// list_tabs_state_test.go — ADR-075 D1 tests 8, 9 and 11.
//
// The subject is one sentence: browser_list_tabs must stop conflating things
// that are not the same. It conflated two of them.
//
//   - WHAT is there. "No browser here at all" and "a browser with nothing
//     open" came back as the identical `nil, 0, nil`, so the model was told
//     "no tabs" in a case where the truthful answer was "I cannot see a
//     browser here" (FR-013).
//   - WHOSE they are. A browser holds one tab set per chat session plus the
//     operator's own; reporting one of them as "the tabs" is the ownership
//     confusion ADR-075 §1.1 records (FR-080).
//
// And one thing that is NOT a state: a policy-denied agent. ADR D1.12
// withdrew the "denied" member as unreachable — the tool never reaches that
// agent at all, and test 11 asserts that ABSENCE rather than a message.

package browser

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// declaredTabStateValues parses pkg/tools/browser's own source for every
// constant declared with type TabState and returns their string values.
//
// Source, not a hand-copied list: a hand-copied list would go stale the day
// someone adds a fourth member, which is precisely the day this assertion
// needs to fire.
func declaredTabStateValues(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, filepath.Join(".", e.Name()), nil, parser.SkipObjectResolution)
		require.NoError(t, perr, e.Name())
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			var currentType string
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				if id, ok := vs.Type.(*ast.Ident); ok {
					currentType = id.Name
				}
				if currentType != "TabState" {
					continue
				}
				for _, v := range vs.Values {
					lit, ok := v.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					unq, uerr := strconv.Unquote(lit.Value)
					require.NoError(t, uerr)
					out = append(out, unq)
				}
			}
		}
	}
	require.NotEmpty(t, out, "no TabState constants found — the parse, not the code, is broken")
	return out
}

// --- test 9 (FR-015 + FR-034) ----------------------------------------------

// interimSharedBrowserLiteral is §3.3's INTERIM literal. It survives at
// EXACTLY ONE site — tools.go's `clear` parameter description — which FR-034a
// says stays interim at both stages.
const interimSharedBrowserLiteral = "the browser this workspace's agents share"

// finalWorkspaceBrowserLiteral is §3.3's FINAL (stage-P) phrasing for the four
// model-visible tab-tool descriptions.
const finalWorkspaceBrowserLiteral = "this workspace's browser"

// finalIsolationLiteral is §3.3's FINAL isolation SENTENCE, added to
// browser_list_tabs and browser_open_tab only.
//
// It makes a CROSS-WORKSPACE claim, and it may only ship in the same commit as
// FR-037 — the change that gives each workspace its own Chrome process and its
// own --user-data-dir profile directory. Before that commit one Chrome served
// every workspace and this sentence was false; a product asserting an
// isolation guarantee it does not have is the exact defect ADR-075 §1.1
// records (MAJ-107).
const finalIsolationLiteral = "Each workspace has its own browser, with its own logins; " +
	"you cannot see or use another workspace's."

// TestToolDescriptions_NoFalseSharedClaim — stage P form.
//
// The assertion that matters is the LAST one, and it is what stops this test
// from becoming decoration: the isolation sentence is only permitted because
// pool.go exists. If the pool were deleted or reverted while the sentence
// stayed, the model would keep being told a guarantee the build no longer
// provides, and every other assertion here would still pass.
func TestToolDescriptions_NoFalseSharedClaim(t *testing.T) {
	sources := packageGoSources(t)

	for name, src := range sources {
		assert.NotContains(t, src, "shared browser session",
			"%s still claims \"shared browser session\" — one browser is no longer shared by every "+
				"agent in the install, and the phrase says nothing about who shares what", name)
	}

	joined := strings.Join([]string{sources["tabs.go"], sources["tools.go"]}, "\n")

	// The four model-visible tab-tool descriptions carry the final phrasing.
	assert.GreaterOrEqual(t, strings.Count(sources["tabs.go"], finalWorkspaceBrowserLiteral), 4,
		"all four tab-tool Description() strings must name \"%s\"", finalWorkspaceBrowserLiteral)

	// The isolation sentence lands on exactly the two tools §3.3 names —
	// browser_list_tabs and browser_open_tab — and nowhere else.
	assert.Equal(t, 2, strings.Count(joined, finalIsolationLiteral),
		"the isolation sentence belongs on browser_list_tabs and browser_open_tab, and on no other tool")

	// FR-034a: tools.go's `clear` parameter description keeps the interim
	// form, deliberately. It describes what the parameter does to a tab set,
	// not who owns the browser, so the isolation claim would be noise there.
	assert.Equal(t, 1, strings.Count(sources["tools.go"], interimSharedBrowserLiteral),
		"tools.go's parameter description keeps the interim literal at both stages (FR-034a)")
	assert.NotContains(t, sources["tabs.go"], interimSharedBrowserLiteral,
		"tabs.go moved to the final literal — the interim one must be gone from it")

	// THE CLAIM MUST NOT OUTLIVE THE BEHAVIOUR.
	//
	// The sentence above promises a per-workspace browser. What makes that
	// true is the pool: one Chrome process and one --user-data-dir per
	// BrowsingKey. Assert the mechanism is present, so that removing it
	// without removing the claim turns this red instead of shipping a
	// guarantee the build does not honour.
	pool, ok := sources["pool.go"]
	require.True(t, ok,
		"pool.go is gone, but the tool descriptions still promise each workspace its own browser — "+
			"that claim is only true while the pool exists (FR-037 + FR-034a are one commit)")
	assert.Contains(t, pool, "func (p *BrowserPool) ProfileDirFor(",
		"the per-workspace profile directory is what gives a workspace its own logins")
	assert.Contains(t, pool, "func (p *BrowserPool) Acquire(",
		"the per-key Chrome launch is what gives a workspace its own browser")
}

// packageGoSources reads every non-test .go file in this package.
func packageGoSources(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, rerr := os.ReadFile(e.Name())
		require.NoError(t, rerr)
		out[e.Name()] = string(b)
	}
	require.NotEmpty(t, out)
	return out
}

func containsToolNamed(defs []tools.Tool, name string) bool {
	for _, d := range defs {
		if d.Name() == name {
			return true
		}
	}
	return false
}
