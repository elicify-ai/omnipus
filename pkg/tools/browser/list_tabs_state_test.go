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
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- test 8 (FR-013) --------------------------------------------------------

// TestListTabsState_ThreeDistinctStates builds each of the three states
// directly on a manager and asserts they are pairwise distinguishable — both
// in ListTabsState's own return and in the payload the model actually reads.
//
// The oracle is §10.2's dataset table, not the implementation:
//
//	no `sessions` entry            -> TabStateNoContext, empty tabs
//	browser live, 2 tabs           -> TabStateOpen,      2 tabs
//	browser live, len(se.tabs)==0  -> TabStateEmpty,     empty tabs
func TestListTabsState_ThreeDistinctStates(t *testing.T) {
	// --- no_context: nothing has ever browsed under this key+owner.
	mNone := newTestManagerWithFakeTabs(t)
	state, tabs, _, err := mNone.ListTabsState(testSessionID)
	require.NoError(t, err, "an absent browsing context is a STATE, never an error")
	assert.Equal(t, TabStateNoContext, state,
		"a manager with no sessions entry must report no_context — reporting an empty tab set here "+
			"is the exact lie FR-013 exists to remove")
	assert.Empty(t, tabs)

	// --- open: a live context with two tabs.
	mOpen := newTestManagerWithFakeTabs(t)
	_, err = mOpen.Session(testSessionID) // creates the context with its first tab
	require.NoError(t, err)
	_, err = mOpen.OpenTab(testSessionID)
	require.NoError(t, err)
	state, tabs, activeIdx, err := mOpen.ListTabsState(testSessionID)
	require.NoError(t, err)
	assert.Equal(t, TabStateOpen, state)
	assert.Len(t, tabs, 2, "§10.2: browser live with 2 tabs")
	assert.GreaterOrEqual(t, activeIdx, 0)

	// --- empty: a live context whose tab set is momentarily zero. Reachable
	// in production through CloseTab's last-tab path when the replacement tab
	// fails to open.
	mEmpty := newTestManagerWithFakeTabs(t)
	_, err = mEmpty.Session(testSessionID)
	require.NoError(t, err)
	mEmpty.mu.Lock()
	mEmpty.sessions[testSessionID].tabs = nil
	mEmpty.mu.Unlock()
	state, tabs, _, err = mEmpty.ListTabsState(testSessionID)
	require.NoError(t, err, "an empty tab set is a STATE, never an error")
	assert.Equal(t, TabStateEmpty, state,
		"a live context with zero tabs must be distinguishable from no context at all")
	assert.Empty(t, tabs)

	// --- the states are pairwise distinct as MODEL-VISIBLE payloads, which is
	// the property that actually matters: three different situations must not
	// render to two different answers.
	payloads := map[string]string{}
	for name, m := range map[string]*BrowserManager{
		"no_context": mNone, "open": mOpen, "empty": mEmpty,
	} {
		tool := &ListTabsTool{res: newFixedResolver(m)}
		res := tool.Execute(context.Background(), map[string]any{})
		require.False(t, res.IsError, "%s: %s", name, res.ForLLM)
		payloads[name] = res.ForLLM
	}
	assert.NotEqual(t, payloads["no_context"], payloads["empty"],
		"no_context and empty must not render identically — that identity IS the reported defect")
	assert.NotEqual(t, payloads["no_context"], payloads["open"])
	assert.NotEqual(t, payloads["empty"], payloads["open"])

	// --- the state set is CLOSED at exactly three, with no "denied" member
	// (ADR D1.12). Enumerated from the package's own source so a fourth
	// constant added later fails here rather than shipping unnoticed.
	assert.ElementsMatch(t,
		[]string{"no_context", "open", "empty"},
		declaredTabStateValues(t),
		"TabState must be exactly {no_context, open, empty}; D1.12 withdrew \"denied\" as unreachable, "+
			"because a policy-denied agent never receives the tool at all (see test 11)")
}

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

// TestListTabs_DelegatesAndNeverReturnsSilentEmpty pins the §5 non-behaviour:
// once ListTabsState exists, ListTabs must not carry its own "missing context
// looks like an empty success" branch. It delegates, so the two agree by
// construction rather than by two copies of the same logic staying in step.
func TestListTabs_DelegatesAndNeverReturnsSilentEmpty(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) *BrowserManager
	}{
		{"no_context", func(t *testing.T) *BrowserManager { return newTestManagerWithFakeTabs(t) }},
		{"open", func(t *testing.T) *BrowserManager {
			m := newTestManagerWithFakeTabs(t)
			_, err := m.Session(testSessionID)
			require.NoError(t, err)
			return m
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.build(t)
			wantState, wantTabs, wantIdx, wantErr := m.ListTabsState(testSessionID)
			gotTabs, gotIdx, gotErr := m.ListTabs(testSessionID)
			assert.Equal(t, wantErr, gotErr)
			assert.Equal(t, wantTabs, gotTabs)
			assert.Equal(t, wantIdx, gotIdx)
			assert.NotEmpty(t, string(wantState))
		})
	}

	// The structural half: the literal `return nil, 0, nil` — the silent
	// empty-success shape — must not exist anywhere in manager.go any more.
	src, err := os.ReadFile("manager.go")
	require.NoError(t, err)
	assert.NotContains(t, string(src), "return nil, 0, nil",
		"manager.go must not return the silent nil,0,nil empty-success shape once ListTabsState exists (§5)")
}

// --- FR-080's payload half --------------------------------------------------

// TestListTabs_PayloadLabelsSessionAndWorkspaceSets asserts the payload says
// WHOSE tabs it is reporting: this chat session's own set, and — separately
// labelled — the workspace-owned set the operator opened.
//
// The negative half is the one that matters. A build that merged the two sets
// into one array, or that reported only the session's and called it "the
// tabs", would pass any assertion that only counts tabs.
func TestListTabs_PayloadLabelsSessionAndWorkspaceSets(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	// The operator opens a tab through the live panel: the workspace-owned set.
	operatorSet := sessionKey(testKey, TabOwnerWorkspace())
	_, err := m.Session(operatorSet)
	require.NoError(t, err)

	// This chat session opens two of its own.
	_, err = m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	tool := &ListTabsTool{res: newFixedResolver(m)}
	out := decodeToolJSON(t, tool.Execute(context.Background(), map[string]any{}))

	assert.Equal(t, "this_chat_session", out["tabs_owner"],
		"the payload must name whose set `tabs` is")
	sessionTabs, ok := out["tabs"].([]any)
	require.True(t, ok, "tabs must be an array: %v", out)
	assert.Len(t, sessionTabs, 2, "`tabs` is THIS session's set — 2 tabs, not the operator's 1 as well")

	operatorTabs, ok := out["operator_tabs"].([]any)
	require.True(t, ok, "the operator's workspace-owned set must be reported separately: %v", out)
	assert.Len(t, operatorTabs, 1)
	assert.Equal(t, string(TabStateOpen), out["operator_tabs_state"])
	assert.Contains(t, out, "tab_ownership", "the payload must say in words which set is which")

	// A second chat session on the SAME browser sees its own (absent) set and
	// the SAME workspace-owned set — never the first session's tabs.
	otherOwner, err := TabOwnerSession("01OTHERSESSION")
	require.NoError(t, err)
	otherTool := &ListTabsTool{res: &fixedResolver{mgr: m, key: testKey, owner: otherOwner}}
	other := decodeToolJSON(t, otherTool.Execute(context.Background(), map[string]any{}))
	assert.Equal(t, string(TabStateNoContext), other["state"],
		"a session that has never browsed sees no_context — not the other session's tabs")
	assert.Empty(t, other["tabs"])
	otherOperatorTabs, ok := other["operator_tabs"].([]any)
	require.True(t, ok)
	assert.Len(t, otherOperatorTabs, 1,
		"the workspace-owned set is visible to every session on the workspace, labelled as the operator's")

	// A turn that IS the operator gets one set, not the same set under two
	// names — an invented second tab set would be its own lie.
	opTool := &ListTabsTool{res: newOperatorResolver(m)}
	op := decodeToolJSON(t, opTool.Execute(context.Background(), map[string]any{}))
	assert.Equal(t, "workspace_operator", op["tabs_owner"])
	assert.NotContains(t, op, "operator_tabs",
		"an operator turn must not have its own set echoed back as a second, separate one")
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

// --- test 11 (FR-014) -------------------------------------------------------

// TestListTabs_DeniedAgentNeverReachesTool asserts the ABSENCE that ADR D1.12
// rules on: a policy-denied agent is never shown browser_list_tabs, so
// Execute is never entered and there is no payload to shape.
//
// FilterToolsByPolicy `continue`s past a deny verdict rather than substituting
// a refusal tool, so the assertion is on the tool DEFINITIONS the model sees.
// There is deliberately no ModelMessage assertion: nothing runs, so nothing
// speaks. This is why TabState has no "denied" member.
func TestListTabs_DeniedAgentNeverReachesTool(t *testing.T) {
	registry := tools.NewToolRegistry()
	m := newTestManagerWithFakeTabs(t)
	require.NoError(t, RegisterTools(registry, newFixedResolver(m), true, t.TempDir(), true))

	all := registry.GetAll()

	// Sanity: with an allow policy the tool IS present. Without this the
	// absence assertion below could pass because nothing was ever registered.
	allowed, _ := tools.FilterToolsByPolicy(all, "custom", &tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"browser_list_tabs": config.ToolPolicyAllow},
	})
	require.True(t, containsToolNamed(allowed, "browser_list_tabs"),
		"precondition: an allowed agent must actually see browser_list_tabs")

	denied, deniedPolicies := tools.FilterToolsByPolicy(all, "custom", &tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"browser_list_tabs": config.ToolPolicyDeny},
	})
	assert.NotContains(t, deniedPolicies, "browser_list_tabs",
		"a denied tool must not even appear in the resolved policy map")
	assert.False(t, containsToolNamed(denied, "browser_list_tabs"),
		"a policy-denied agent must never receive the browser_list_tabs definition — it answers from "+
			"the tool's absence, which is why there is no \"denied\" TabState (ADR D1.12)")
}

func containsToolNamed(defs []tools.Tool, name string) bool {
	for _, d := range defs {
		if d.Name() == name {
			return true
		}
	}
	return false
}
