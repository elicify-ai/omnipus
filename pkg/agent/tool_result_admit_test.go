// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// tool_result_admit_test.go — ADR-066 D4 choke point (spec tests 7–11,
// B-11, B-11b, B-11c, B-12, B-12b, B-13, B-16, B-16b; datasets DS-1 #1–#16).
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// largeBudgetPolicy is the shipped caps with a budget big enough that the
// half-budget clamp never binds (W = 1,048,576-class window).
func largeBudgetPolicy() resultCapPolicy {
	return capPolicyFor(config.DefaultContextSettings(), 400_000)
}

// noMark is a mark source for tests that only care about sizes.
func noMark(string) string { return "[mark]" }

func runes(s string) int { return utf8.RuneCountInString(s) }

// assertCappedForm checks the D4 over-cap shape: total ≤ cap, the mark is
// inside, head and tail are taken from the original, no rune is split.
func assertCappedForm(t *testing.T, original, window, mark string, capChars int) {
	t.Helper()
	require.True(t, utf8.ValidString(window), "no rune may be split (E5)")
	assert.LessOrEqual(t, runes(window), capChars, "window form must not exceed the cap")
	assert.Contains(t, window, mark, "the mark is part of the window form and counts toward the cap")
	headEnd := strings.Index(window, "\n"+mark)
	require.Greater(t, headEnd, 0, "head precedes the mark")
	head := window[:headEnd]
	tail := window[headEnd+len("\n"+mark+"\n"):]
	assert.True(t, strings.HasPrefix(original, head), "head is the original's prefix")
	assert.True(t, strings.HasSuffix(original, tail), "tail is the original's suffix")
	// 50/50: head and tail within one rune of each other.
	assert.InDelta(t, runes(head), runes(tail), 1, "head-and-tail split is 50/50")
}

// Test 7 — B-11 surface table (DS-1 #1–#9). The surface decides the
// configured cap; IsError folds MCP failures onto the builtin-failure cap;
// denied/skipped/delegate/attachment have no exemption.
func TestChokePoint_PerSurfaceCap(t *testing.T) {
	policy := largeBudgetPolicy()
	cases := []struct {
		name     string
		tool     string
		isError  bool
		size     int
		wantCap  int
		wantCapd bool
	}{
		{"mcp success at cap", "mcp_srv_search", false, 62_500, 62_500, false},
		{"mcp success cap+1", "mcp_srv_search", false, 62_501, 62_500, true},
		{"mcp success incident", "mcp_srv_search", false, 1_178_522, 62_500, true},
		{"builtin success at cap", "read_file", false, 64_000, 64_000, false},
		{"builtin success cap+1", "read_file", false, 64_001, 64_000, true},
		{"builtin success 200k", "read_file", false, 200_000, 64_000, true},
		{"builtin failure at cap", "bash", true, 10_000, 10_000, false},
		{"builtin failure cap+1", "bash", true, 10_001, 10_000, true},
		{"builtin failure 50k", "bash", true, 50_000, 10_000, true},
		{"mcp isError 50k", "mcp_srv_search", true, 50_000, 10_000, true},
		{"delegate report 200k", "delegate", false, 200_000, 64_000, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			surface := toolResultSurfaceFor(tc.tool, tc.isError)
			assert.Equal(t, tc.wantCap, policy.effectiveCap(surface, 1), "configured cap for %s", surface)
			content := strings.Repeat("x", tc.size)
			window, capped := projectToolResult(content, policy.effectiveCap(surface, 1), noMark)
			assert.Equal(t, tc.wantCapd, capped)
			if !tc.wantCapd {
				assert.Equal(t, content, window, "at or under the cap the result is unmodified (E1)")
				return
			}
			assertCappedForm(t, content, window, "[mark]", tc.wantCap)
		})
	}

	t.Run("denied and skipped are the failure surface (DS-1 #7)", func(t *testing.T) {
		assert.Equal(t, surfaceBuiltinFailure, toolResultSurfaceFor("bash", true))
		assert.Equal(t, 10_000, policy.effectiveCap(surfaceBuiltinFailure, 1))
	})
	t.Run("hydrated attachment is the builtin-success surface (DS-1 #9)", func(t *testing.T) {
		assert.Equal(t, 64_000, policy.effectiveCap(surfaceBuiltinSuccess, 1))
	})
	t.Run("4-byte runes at the cut are never split (DS-1 #10)", func(t *testing.T) {
		content := strings.Repeat("😀", 62_501)
		window, capped := projectToolResult(content, 62_500, noMark)
		require.True(t, capped)
		assertCappedForm(t, content, window, "[mark]", 62_500)
	})
	t.Run("unset caps fall back to the shipped defaults, never to zero", func(t *testing.T) {
		p := capPolicyFor(config.ContextSettings{}, 400_000)
		assert.Equal(t, 62_500, p.effectiveCap(surfaceMCP, 1))
		assert.Equal(t, 64_000, p.effectiveCap(surfaceBuiltinSuccess, 1))
		assert.Equal(t, 10_000, p.effectiveCap(surfaceBuiltinFailure, 1))
	})
}

// Test 8 — B-11b / B-11c (DS-1 #14, #15): effective_cap =
// min(configured, floor(0.5 × B × 2.5)); N parallel calls that would not
// fit split it /N.
func TestChokePoint_ClampToHalfBudget(t *testing.T) {
	policy := capPolicyFor(config.DefaultContextSettings(), 3_000)

	t.Run("single result clamps to half the budget", func(t *testing.T) {
		assert.Equal(t, 3_750, policy.effectiveCap(surfaceBuiltinSuccess, 1))
		assert.Equal(t, 3_750, policy.effectiveCap(surfaceMCP, 1))
		assert.Equal(t, 3_750, policy.effectiveCap(surfaceBuiltinFailure, 1), "clamp applies to every surface; failure cap 10,000 > 3,750")
		window, capped := projectToolResult(strings.Repeat("y", 64_000), policy.effectiveCap(surfaceBuiltinSuccess, 1), noMark)
		require.True(t, capped)
		assert.LessOrEqual(t, runes(window), 3_750)
	})
	t.Run("three parallel calls split the effective cap", func(t *testing.T) {
		assert.Equal(t, 1_250, policy.effectiveCap(surfaceBuiltinSuccess, 3))
		// 3 × 1,250 chars ≈ 1,500 tokens — the three together fit in B = 3,000.
		assert.LessOrEqual(t, 3*1_250*2/5, 3_000)
	})
	t.Run("parallel calls that already fit are not split", func(t *testing.T) {
		// 2 × 3,750 × 0.4 = 3,000, not > B → no split.
		assert.Equal(t, 3_750, policy.effectiveCap(surfaceBuiltinSuccess, 2))
	})
	t.Run("large budget: configured cap wins, parallel never binds", func(t *testing.T) {
		p := largeBudgetPolicy()
		assert.Equal(t, 64_000, p.effectiveCap(surfaceBuiltinSuccess, 1))
		assert.Equal(t, 64_000, p.effectiveCap(surfaceBuiltinSuccess, 3))
	})
	t.Run("unknown budget (exempt provider) means no clamp", func(t *testing.T) {
		p := capPolicyFor(config.DefaultContextSettings(), 0)
		assert.Equal(t, 64_000, p.effectiveCap(surfaceBuiltinSuccess, 3))
	})
	t.Run("the mark itself counts toward the cap", func(t *testing.T) {
		mark := strings.Repeat("M", 500)
		window, capped := projectToolResult(strings.Repeat("z", 10_000), 1_250, func(string) string { return mark })
		require.True(t, capped)
		assert.LessOrEqual(t, runes(window), 1_250)
		assert.Contains(t, window, mark)
	})
}

// Test 9 — B-12b (DS-1 #16): 8,000,000 newline bytes encode to 16 MB of
// "\n" escapes; the archived line must be cut to ≤ 0.8 × maxLineSize and
// the session must still be readable.
func TestChokePoint_EncodedLineBound(t *testing.T) {
	content := strings.Repeat("\n", 8_000_000)
	msg := providers.Message{Role: "tool", ToolCallID: "call_big", Content: content}

	bounded, origRunes, cut, overflow := boundArchivedMessage(msg)
	require.True(t, cut, "16 MB encoded line must be cut")
	require.False(t, overflow, "content alone can always be shrunk to fit")
	assert.Equal(t, 8_000_000, origRunes)
	encoded, err := json.Marshal(memory.ArchivedMessage{Message: bounded, TS: 1_700_000_000_000})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(encoded), memory.EncodedLineBound, "encoded line ≤ 0.8 × maxLineSize")
	assert.Equal(t, 8_388_608, memory.EncodedLineBound)
	assert.True(t, utf8.ValidString(bounded.Content))
	assert.Equal(t, "call_big", bounded.ToolCallID, "only the content is cut")

	t.Run("a line under the bound is untouched", func(t *testing.T) {
		small := providers.Message{Role: "tool", ToolCallID: "c", Content: strings.Repeat("a", 1_000_000)}
		got, _, cut, overflow := boundArchivedMessage(small)
		assert.False(t, cut)
		assert.False(t, overflow)
		assert.Equal(t, small, got)
	})

	t.Run("the bounded line is what reaches the archive and GetHistory reads it back", func(t *testing.T) {
		al, ts, store := newChokePointTurn(t, 400_000)
		seedAssistantCall(t, store, ts.sessionKey, "call_big", "bash", 1)
		admitted := al.admitToolResult(ts, toolResultAdmission{
			Tool: "bash", ToolCallID: "call_big", Content: content, ParallelN: 1,
		})
		archive, err := store.ReadArchive(context.Background(), ts.sessionKey)
		require.NoError(t, err)
		last := archive[len(archive)-1]
		enc, _ := json.Marshal(last)
		assert.LessOrEqual(t, len(enc), memory.EncodedLineBound)
		assert.Equal(t, len(archive)-1, admitted.ArchiveLine)
		hist := store.GetHistory(ts.sessionKey)
		require.NotEmpty(t, hist, "GetHistory must still read the session")
		assert.Equal(t, last.Content, hist[len(hist)-1].Content)
		assert.True(t, admitted.Capped)
		assert.LessOrEqual(t, runes(admitted.Message.Content), 64_000, "window form ≤ the builtin-success cap")
	})
}

// Test 10 — B-16 (DS-1 #12, #13): the sensitive-data filter runs on the
// FULL content before the cut, so a secret straddling the head cut or the
// tail cut is redacted in both the archive and the window — no fragment.
func TestChokePoint_FilterThenCap_AtRealCuts(t *testing.T) {
	const secret = "SECRET-TOKEN-abcdef0123456789-XYZ"
	al, ts, store := newChokePointTurn(t, 400_000)
	al.GetConfig().Tools.FilterSensitiveData = true // "Given the sensitive-data filter is on" (US-3.AC10)
	al.GetConfig().RegisterSensitiveValues([]string{secret})
	seedAssistantCall(t, store, ts.sessionKey, "call_s", "read_file", 1)

	policy := capPolicyFor(al.GetConfig().Context, agentContextBudget(ts.agent))
	capChars := policy.effectiveCap(surfaceBuiltinSuccess, 1)
	require.Equal(t, 64_000, capChars)

	// Build a 100,000-char body with the secret straddling each cut. The head
	// cut lands around (cap − markLen)/2 ≈ 31,800; the tail cut mirrors it
	// from the end. Planting the secret across a ±200 band around each
	// guarantees it straddles whichever exact position the mark length yields.
	body := []byte(strings.Repeat("a", 100_000))
	plant := func(at int) { copy(body[at:], secret) }
	for at := 31_600; at < 32_200; at += len(secret) + 5 {
		plant(at)
	}
	for at := 100_000 - 32_200; at < 100_000-31_600; at += len(secret) + 5 {
		plant(at)
	}
	content := string(body)
	require.Contains(t, content, secret)

	admitted := al.admitToolResult(ts, toolResultAdmission{
		Tool: "read_file", ToolCallID: "call_s", Content: content, ParallelN: 1,
	})
	require.True(t, admitted.Capped)
	assert.NotContains(t, admitted.Message.Content, secret, "window form must carry no secret")
	assert.Contains(t, admitted.Message.Content, "[FILTERED]")
	// No fragment: a cut inside the secret would leave a prefix/suffix of it.
	for _, frag := range []string{"SECRET-TOKEN-abc", "0123456789-XYZ"} {
		assert.NotContains(t, admitted.Message.Content, frag, "no secret fragment at a cut")
	}
	archive, err := store.ReadArchive(context.Background(), ts.sessionKey)
	require.NoError(t, err)
	archived := archive[len(archive)-1].Content
	assert.NotContains(t, archived, secret, "archive holds the FILTERED full content (FR-013)")
	assert.Contains(t, archived, "[FILTERED]")
	assert.Equal(t, runes(strings.ReplaceAll(content, secret, "[FILTERED]")), runes(archived), "archive is the full filtered content, not the capped form")
}

// chokePointAllowedFiles are the only non-test files in pkg/agent permitted
// to construct a role:"tool" providers.Message directly: the choke point
// itself and the exempt orphan-repair placeholder (bounded by construction).
var chokePointAllowedFiles = map[string]bool{
	"tool_result_admit.go": true, // the choke point
	"repair.go":            true, // exempt: orphan-repair placeholder, bounded by construction
}

// attachHydrateExemptFunc is the name of the one function in
// attach_hydrate.go permitted to call toolResultMessage() directly — see
// the exemption's full rationale where it is applied, in
// scanChokePointBypasses.
const attachHydrateExemptFunc = "hydratedToolResultMessage"

// chokePointViolation is one structural finding from an FR-009 AST scan.
type chokePointViolation struct {
	file string
	pos  string
	kind string
}

// packageGoFiles parses every non-test .go file directly under dir and
// returns them keyed by base filename.
func packageGoFiles(t *testing.T, fset *token.FileSet, dir string) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	files := map[string]*ast.File{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		require.NoErrorf(t, perr, "parsing %s", name)
		files[name] = f
	}
	return files
}

// packageStringConstants collects every same-package const/var declared with
// a bare string-literal value (`const roleTool = "tool"`), across every file
// in files, so a Role field set to an IDENTIFIER can be resolved to the
// value it actually carries — not just the ones spelled as a literal.
func packageStringConstants(files map[string]*ast.File) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					if v, uerr := strconv.Unquote(lit.Value); uerr == nil {
						out[name.Name] = v
					}
				}
			}
		}
	}
	return out
}

// scanChokePointBypasses walks the ACTUAL syntax tree of every non-test .go
// file in dir and reports every structural way a role:"tool"
// providers.Message can reach the window OUTSIDE chokePointAllowedFiles
// (FR-009). Unlike a regex over raw source text (the previous form of this
// check), this is immune to:
//
//   - reformatting (extra whitespace inside `Role:  "tool"` — regex already
//     tolerated this one, but the failure modes below did not).
//   - calling the choke point's OWN constructor (toolResultMessage) from a
//     DIFFERENT file, instead of writing the struct literal directly —
//     zero occurrences of the literal string "tool" appear in that file.
//   - hiding the literal behind a same-package string constant/var
//     (`const roleTool = "tool"`; `Role: roleTool`) — resolved via
//     packageStringConstants, so the alias is not a hiding place.
//   - a plain field assignment after construction (`m.Role = "tool"`) —
//     which is not a composite-literal field at all, so a literal-scan
//     regex never saw it.
//   - a producer in ANY file (every non-test file is scanned, not just
//     loop.go).
func scanChokePointBypasses(t *testing.T, dir string) []chokePointViolation {
	t.Helper()
	fset := token.NewFileSet()
	files := packageGoFiles(t, fset, dir)
	stringConsts := packageStringConstants(files)

	resolvesToTool := func(e ast.Expr) bool {
		switch v := e.(type) {
		case *ast.BasicLit:
			if v.Kind == token.STRING {
				if s, uerr := strconv.Unquote(v.Value); uerr == nil {
					return s == "tool"
				}
			}
		case *ast.Ident:
			return stringConsts[v.Name] == "tool"
		}
		return false
	}
	isProvidersMessageType := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == "providers" && sel.Sel.Name == "Message"
	}

	var violations []chokePointViolation
	for name, f := range files {
		if chokePointAllowedFiles[name] {
			continue
		}
		// attachHydrateExemptFunc (attach_hydrate.go's hydratedToolResultMessage)
		// is the ONE function anywhere outside the choke point permitted to
		// call toolResultMessage() directly: it reconstructs a tool message
		// from an ALREADY-transcript-recorded call during session hydration
		// — there is no live archive to append to; the transcript IS the
		// durable record — and, unlike a genuine bypass, its content is
		// routed through the choke point's OWN cap function
		// (projectToolResult) first. The exemption is scoped to THIS ONE
		// FUNCTION, never the whole file: any other function in
		// attach_hydrate.go calling toolResultMessage, or this function
		// doing so without ALSO calling projectToolResult, still violates.
		if name == "attach_hydrate.go" {
			ast.Inspect(f, func(n ast.Node) bool {
				fn, ok := n.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					return true
				}
				callsToolResultMessage, callsProjectToolResult := false, false
				var toolResultMessageCallPos token.Pos
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					id, ok := call.Fun.(*ast.Ident)
					if !ok {
						return true
					}
					switch id.Name {
					case "toolResultMessage":
						callsToolResultMessage = true
						toolResultMessageCallPos = call.Pos()
					case "projectToolResult":
						callsProjectToolResult = true
					}
					return true
				})
				if !callsToolResultMessage {
					return true
				}
				if fn.Name.Name == attachHydrateExemptFunc && callsProjectToolResult {
					return true // the one sanctioned, capped call site
				}
				violations = append(violations, chokePointViolation{
					file: name, pos: fset.Position(toolResultMessageCallPos).String(),
					kind: fmt.Sprintf(
						"calls toolResultMessage() outside the choke point file, in %s "+
							"(only %s calling projectToolResult first is exempt)",
						fn.Name.Name, attachHydrateExemptFunc),
				})
				return true
			})
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CallExpr:
				if name == "attach_hydrate.go" {
					break // handled above with function-scoped context
				}
				if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "toolResultMessage" {
					violations = append(violations, chokePointViolation{
						file: name, pos: fset.Position(v.Pos()).String(),
						kind: "calls toolResultMessage() outside the choke point file",
					})
				}
			case *ast.CompositeLit:
				if !isProvidersMessageType(v.Type) {
					return true
				}
				for _, elt := range v.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok || key.Name != "Role" {
						continue
					}
					if resolvesToTool(kv.Value) {
						violations = append(violations, chokePointViolation{
							file: name, pos: fset.Position(v.Pos()).String(),
							kind: "constructs a role:tool providers.Message literal outside the choke point",
						})
					}
				}
			case *ast.AssignStmt:
				for i, lhs := range v.Lhs {
					sel, ok := lhs.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Role" || i >= len(v.Rhs) {
						continue
					}
					if resolvesToTool(v.Rhs[i]) {
						violations = append(violations, chokePointViolation{
							file: name, pos: fset.Position(v.Pos()).String(),
							kind: `assigns .Role = "tool" outside the choke point`,
						})
					}
				}
			}
			return true
		})
	}
	return violations
}

// identFieldReferencedAfter reports whether varName.field is referenced
// anywhere in body at a position strictly after `after`.
func identFieldReferencedAfter(body ast.Node, varName, field string, after token.Pos) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Pos() <= after {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == varName && sel.Sel.Name == field {
			found = true
			return false
		}
		return true
	})
	return found
}

// scanDiscardedChokePointResults finds every `X := al.admitToolResult(...)`
// (or `X, ... :=`) in path whose result's .Message field is never
// referenced anywhere later in the SAME enclosing function — the "keep the
// call, but build the appended message some other way" bypass, which a
// call-COUNT check cannot see: the count stays the same whether or not the
// returned, capped/filtered/archived message is what actually gets used.
func scanDiscardedChokePointResults(t *testing.T, path string) []chokePointViolation {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	require.NoError(t, err)

	var violations []chokePointViolation
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, rhs := range as.Rhs {
				call, ok := rhs.(*ast.CallExpr)
				if !ok {
					continue
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "admitToolResult" {
					continue
				}
				if i >= len(as.Lhs) {
					continue
				}
				varName, ok := as.Lhs[i].(*ast.Ident)
				if !ok || varName.Name == "_" {
					continue
				}
				if !identFieldReferencedAfter(fn.Body, varName.Name, "Message", as.Pos()) {
					violations = append(violations, chokePointViolation{
						file: filepath.Base(path), pos: fset.Position(call.Pos()).String(),
						kind: "admitToolResult() result's .Message is never used (var " + varName.Name + ")",
					})
				}
			}
			return true
		})
		return true
	})
	return violations
}

// fileCallsFunction reports whether path's AST contains an ACTUAL call to
// funcName — a bare call (`funcName(...)`) or a method/selector call
// (`x.funcName(...)`). Unlike a raw substring scan over the file's text, a
// mention of funcName inside a comment does not satisfy this: comments are
// stored separately from the expression tree, so ast.Inspect never visits
// them.
func fileCallsFunction(t *testing.T, path, funcName string) bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	require.NoError(t, err)
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if fn.Name == funcName {
				found = true
			}
		case *ast.SelectorExpr:
			if fn.Sel.Name == funcName {
				found = true
			}
		}
		return true
	})
	return found
}

// Test 11 — FR-009: every role:"tool" producer routes through the choke
// point. M4: this used to be enforced by a source-text regex
// (`Role:\s*"tool"`), which is grep-shaped rather than property-shaped — a
// 2026-08-27 review found five distinct edits that keep a pure text/
// substring scan green while an uncapped, unarchived tool result reaches
// the window: calling the choke
// point's own constructor from a new file, hiding the literal behind a
// same-package string constant, a post-construction field assignment, a
// call whose result is thrown away without the count changing, and a
// comment-only mention of the required call. scanChokePointBypasses,
// scanDiscardedChokePointResults and fileCallsFunction replace the text
// scan with an AST walk that closes all five: it constrains the PROPERTY
// (every role:tool providers.Message construction, and every choke-point
// call's actual use), not the spelling of one particular way to violate it.
// The name is kept (rather than renamed to e.g. …ProducerListStructural)
// because production comments in this package cite it by name
// (tool_result_admit.go, repair.go, recall_injection.go) and the ADR-066
// spec docs this task is not scoped to edit cite it too; only this
// function's OWN doc comment needed to stop claiming "grep".
func TestChokePoint_ProducerListByGrep(t *testing.T) {
	violations := scanChokePointBypasses(t, ".")
	vmsgs := make([]string, 0, len(violations))
	for _, v := range violations {
		vmsgs = append(vmsgs, fmt.Sprintf("%s (%s): %s", v.file, v.pos, v.kind))
	}
	assert.Empty(t, vmsgs, "files constructing role:tool messages outside the choke point (FR-009):\n%s", strings.Join(vmsgs, "\n"))

	discarded := scanDiscardedChokePointResults(t, "loop.go")
	dmsgs := make([]string, 0, len(discarded))
	for _, v := range discarded {
		dmsgs = append(dmsgs, fmt.Sprintf("%s: %s", v.pos, v.kind))
	}
	assert.Empty(t, dmsgs, "admitToolResult() call(s) whose result is discarded in loop.go:\n%s", strings.Join(dmsgs, "\n"))

	// A floor, not the enforcement mechanism: scanChokePointBypasses and
	// scanDiscardedChokePointResults above are what actually prove the
	// property. This just confirms no call site was silently deleted; a
	// legitimate new call site is expected to raise the floor, not fail it.
	loopSrc, err := os.ReadFile("loop.go")
	require.NoError(t, err)
	calls := strings.Count(string(loopSrc), "al.admitToolResult(ts,")
	assert.GreaterOrEqual(t, calls, 10, "loop.go: success path + seven denied sites + skipped site + the T066-15 argument-refusal site (FR-016) = at least 10 choke-point calls")

	for _, fname := range []string{"attach_hydrate.go", "recall_conversation.go"} {
		assert.True(t, fileCallsFunction(t, fname, "projectToolResult"),
			"%s must actually CALL the choke point's cap (projectToolResult), not merely mention it", fname)
	}
	repairSrc, err := os.ReadFile("repair.go")
	require.NoError(t, err)
	assert.Contains(t, strings.ToLower(string(repairSrc)), "exempt from the choke point", "repair.go must annotate its exemption")
}

// B-12 / DS-1 #3: the incident-size MCP result enters at ≤ 62,500 chars, the
// archive line holds the full content, meta records (id, line) → capped,
// and the projection re-derives the capped form byte-identical on reload.
func TestChokePoint_IncidentResult_FullInArchiveCappedOnReload(t *testing.T) {
	al, ts, store := newChokePointTurn(t, 400_000)
	seedAssistantCall(t, store, ts.sessionKey, "call_inc", "mcp_gmail_search_email", 1)
	content := strings.Repeat("incident ", 130_947) // 1,178,523 chars
	content = content[:1_178_522]

	admitted := al.admitToolResult(ts, toolResultAdmission{
		Tool: "mcp_gmail_search_email", ToolCallID: "call_inc", Content: content, ParallelN: 1,
	})
	require.True(t, admitted.Capped)
	assert.LessOrEqual(t, runes(admitted.Message.Content), 62_500)
	assert.Equal(t, "call_inc", admitted.Message.ToolCallID)
	assert.Equal(t, "tool", admitted.Message.Role)
	assert.Contains(t, admitted.Message.Content, `"content_state":"capped"`)
	assert.Contains(t, admitted.Message.Content, `"size_chars":1178522`)

	archive, err := store.ReadArchive(context.Background(), ts.sessionKey)
	require.NoError(t, err)
	require.Len(t, archive, 3, "user, assistant, tool")
	assert.Equal(t, content, archive[2].Content, "archive holds the full content")
	assert.Equal(t, 2, admitted.ArchiveLine)

	pm := store.Projection(ts.sessionKey)
	assert.Equal(t, memory.ProjectionCapped, pm.Entries[memory.ProjectionKey{ToolCallID: "call_inc", ArchiveLine: 2}])

	// Reload: the projection function applied to GetHistory yields the same bytes.
	history := store.GetHistory(ts.sessionKey)
	skip := len(archive) - len(history)
	projected := projectMessages(history, func(i int) int { return skip + i }, pm.Entries, projectionContext{
		policy:  capPolicyFor(al.GetConfig().Context, agentContextBudget(ts.agent)),
		archive: archive,
	})
	assert.Equal(t, admitted.Message.Content, projected[2].Content, "reload renders the capped form byte-identical (B-12)")
	assert.Equal(t, content, history[2].Content, "projection never mutates its input")
}

// B-13 (DS-1 #11): over the warn threshold but under the cap → unmodified,
// one WARN and tool_result_large_total increments.
func TestChokePoint_WarnThresholdObserveOnly(t *testing.T) {
	al, ts, store := newChokePointTurn(t, 400_000)
	seedAssistantCall(t, store, ts.sessionKey, "call_w", "read_file", 1)
	before := ToolResultLargeTotal()
	content := strings.Repeat("w", 25_001)
	admitted := al.admitToolResult(ts, toolResultAdmission{Tool: "read_file", ToolCallID: "call_w", Content: content, ParallelN: 1})
	assert.False(t, admitted.Capped)
	assert.Equal(t, content, admitted.Message.Content)
	assert.Equal(t, before+1, ToolResultLargeTotal())
	pm := store.Projection(ts.sessionKey)
	assert.Empty(t, pm.Entries, "no projection state for an unmodified result")

	// Exactly at the threshold: no increment.
	seedAssistantCall(t, store, ts.sessionKey, "call_w2", "read_file", 1)
	al.admitToolResult(ts, toolResultAdmission{Tool: "read_file", ToolCallID: "call_w2", Content: strings.Repeat("w", 25_000), ParallelN: 1})
	assert.Equal(t, before+1, ToolResultLargeTotal())
}

// B-16b: settings are read per call — a cap lowered mid-turn applies to the
// next result.
func TestChokePoint_LiveSettingsPerCall(t *testing.T) {
	al, ts, store := newChokePointTurn(t, 400_000)
	seedAssistantCall(t, store, ts.sessionKey, "c1", "read_file", 1)
	first := al.admitToolResult(ts, toolResultAdmission{Tool: "read_file", ToolCallID: "c1", Content: strings.Repeat("a", 30_000), ParallelN: 1})
	assert.False(t, first.Capped)

	al.GetConfig().Context.BuiltinSuccessCap = 20_000
	seedAssistantCall(t, store, ts.sessionKey, "c2", "read_file", 1)
	second := al.admitToolResult(ts, toolResultAdmission{Tool: "read_file", ToolCallID: "c2", Content: strings.Repeat("a", 30_000), ParallelN: 1})
	assert.True(t, second.Capped, "the lowered cap applies to the next result")
	assert.LessOrEqual(t, runes(second.Message.Content), 20_000)
}

// --- helpers -------------------------------------------------------------

// newChokePointTurn builds a loop, a JSONL-backed session store and a
// turnState bound to it, with a context window that yields budget ≈ the
// requested value (MaxTokens and pinned overhead subtracted).
func newChokePointTurn(t *testing.T, window int) (*AgentLoop, *turnState, session.SessionStore) {
	t.Helper()
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	t.Cleanup(cleanup)
	cfg.Context = config.DefaultContextSettings()
	store := session.NewSessionManager(t.TempDir())
	agent := &AgentInstance{
		ID:            "choke-agent",
		Name:          "Choke",
		Sessions:      store,
		ContextWindow: window,
		MaxTokens:     1_000,
	}
	ts := &turnState{
		agent:      agent,
		agentID:    agent.ID,
		turnID:     "turn-choke",
		sessionKey: "agent:choke-agent:session:s1",
		opts:       processOptions{SessionKey: "agent:choke-agent:session:s1"},
	}
	return al, ts, store
}

// seedAssistantCall writes the user + assistant(tool call) lines a tool
// result always follows, so the archive has the owning call for the mark
// and the projection lookup.
func seedAssistantCall(t *testing.T, store session.SessionStore, key, callID, tool string, n int) {
	t.Helper()
	store.AddMessage(key, "user", "go")
	calls := make([]providers.ToolCall, 0, n)
	for i := 0; i < n; i++ {
		id := callID
		if i > 0 {
			id = callID + "_" + string(rune('a'+i))
		}
		calls = append(calls, providers.ToolCall{ID: id, Type: "function", Name: tool, Function: &providers.FunctionCall{Name: tool, Arguments: "{}"}})
	}
	store.AddFullMessage(key, providers.Message{Role: "assistant", ToolCalls: calls})
}

// TestChokePoint_MediaOverflowIsBounded pins the Media half of FR-012.
//
// The regression: the shrink loop only ever touched msg.Content, but
// encodedArchiveLineLen measures Media too — attachToolResultMedia inlines
// base64 data URLs bounded only by max_media_size (20 MB by default). With
// the overflow in Media the loop drove `keep` to 0 and then made no further
// progress: it destroyed the ENTIRE textual result (the model saw "\n" too,
// since contentForLLM is the archived content) and still returned
// "cut: true" for a line megabytes over the store's own 10 MB maxLineSize.
// Appending it broke every later read of that session with bufio.Scanner
// ErrTooLong, and GetHistory answered with an empty slice — the session's
// whole history silently and permanently gone.
func TestChokePoint_MediaOverflowIsBounded(t *testing.T) {
	// One ~10.7 MB base64 data URL, the shape attachToolResultMedia produces
	// for an ~8 MB screenshot.
	big := "data:image/png;base64," + strings.Repeat("A", 10_700_000)
	msg := providers.Message{
		Role:       "tool",
		ToolCallID: "call_shot",
		Content:    "screenshot of the login page, 1280x720",
		Media:      []string{big},
	}

	bounded, origRunes, cut, overflow := boundArchivedMessage(msg)
	require.True(t, cut, "the line is over the bound and must be reported as cut")
	assert.False(t, overflow,
		"dropping the media must bring the line under the bound — a still-over line would break the session")
	assert.Equal(t, utf8.RuneCountInString(msg.Content), origRunes)

	encoded, err := json.Marshal(memory.ArchivedMessage{Message: bounded, TS: 1_700_000_000_000})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(encoded), memory.EncodedLineBound,
		"the encoded archive line must fit the bound once media is accounted for")
	assert.Empty(t, bounded.Media, "the oversize media entry is dropped, largest first")
	assert.Equal(t, msg.Content, bounded.Content,
		"the textual result must survive: the overflow was in Media, and destroying the text "+
			"neither fixed the line nor left the model anything to work with")

	t.Run("the smallest media entries survive when dropping the largest is enough", func(t *testing.T) {
		small := "data:image/png;base64," + strings.Repeat("B", 1_000)
		multi := providers.Message{
			Role:       "tool",
			ToolCallID: "call_multi",
			Content:    "two screenshots",
			Media:      []string{small, big, small},
		}
		got, _, cut, overflow := boundArchivedMessage(multi)
		require.True(t, cut)
		assert.False(t, overflow)
		assert.Equal(t, []string{small, small}, got.Media, "only the oversize entry goes")
		assert.Equal(t, multi.Content, got.Content)
	})
}

// TestChokePoint_FailureSurfaceCapSurvivesReload pins FR-019 / US-6.AC3 /
// B-12 / B-22 for the failure surface: the bytes assembled on reload for a
// capped result must be IDENTICAL to the bytes the model saw live.
//
// The regression: memory.ProjectionState recorded only "capped", never which
// D4 surface produced the cut, and projectMessages hard-coded isError=false.
// A failed MCP call cut to builtin_failure_cap (10,000) live therefore
// re-projected at the MCP success cap (62,500) on reload — 6.25x the bytes
// the model was given, on the surface deliberately capped tightest.
func TestChokePoint_FailureSurfaceCapSurvivesReload(t *testing.T) {
	al, ts, store := newChokePointTurn(t, 400_000)
	const (
		tool   = "mcp_github_search"
		callID = "call_fail"
	)
	seedAssistantCall(t, store, ts.sessionKey, callID, tool, 1)

	// A 200,000-char error payload: over both the failure cap (10,000) and
	// the MCP success cap (62,500), so the two cuts are distinguishable.
	body := strings.Repeat("lorem ipsum ", 200_000/len("lorem ipsum "))
	admitted := al.admitToolResult(ts, toolResultAdmission{
		Tool: tool, ToolCallID: callID, Content: body, ParallelN: 1, IsError: true,
	})
	require.True(t, admitted.Capped, "precondition: the failure payload is over its cap")
	require.Equal(t, config.DefaultBuiltinFailureCap, admitted.EffectiveCap,
		"a failed call is cut at the builtin-failure cap, not the MCP success cap")
	liveBytes := admitted.Message.Content
	require.LessOrEqual(t, runes(liveBytes), config.DefaultBuiltinFailureCap)

	// The recorded state must say WHICH cap produced those bytes.
	set := store.Projection(ts.sessionKey).Entries
	key := memory.ProjectionKey{ToolCallID: callID, ArchiveLine: admitted.ArchiveLine}
	require.Equal(t, memory.ProjectionCappedFailure, set[key],
		"the failure surface must be part of the persisted state — the window carries no IsError")

	// Reload: assemble from the archive through the ONE projection function.
	archive, err := store.ReadArchive(context.Background(), ts.sessionKey)
	require.NoError(t, err)
	reloadMsgs := make([]providers.Message, len(archive))
	for i := range archive {
		reloadMsgs[i] = archive[i].Message
	}
	lineOf := archiveLineResolver(archive, reloadMsgs)
	projected := projectMessages(reloadMsgs, lineOf, set, projectionContext{
		policy:  capPolicyFor(config.DefaultContextSettings(), agentContextBudget(ts.agent)),
		archive: archive,
	})

	assert.Equal(t, liveBytes, projected[admitted.ArchiveLine].Content,
		"B-22: the reloaded bytes must be byte-identical to what the model saw live — "+
			"re-cutting a failure at the success cap shows 6.25x more content")
}
