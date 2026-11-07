package tools

// RED tests — landing/skills (T9): the T-E9b retired-tools gate, §15 items
// 1–3 (machine-verifiable constraints; SC-002). "Gone as tools, not as
// words": create_skill and remove_skill must appear in none of the
// registration/policy surfaces this package owns, no type in pkg/ may
// implement a tool with either name, no writer of .skill-origin.json may
// remain (§15 item 2), and the symbol-boundary text gate (§15 item 3) must
// find none of the seven gone-forever symbols outside _test.go files — with
// the r3 feasibility assertions proving the gate rejects a real leftover
// skills symbol and accepts the four unrelated live workspace identifiers.
// Traces to: skills-spec-r15.md §15 items 1–3, §17.1 row 4, SC-002.
//
// RED expectation: create_skill/remove_skill still exist as registered
// tools, auto-approve entries, a golden-definitions row and an
// .skill-origin.json writer, and SeedDefaults is still live — every
// behavioural assertion below fails on the baseline.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// retiredToolNamesUnderGate are the two names §15 retires.
var retiredToolNamesUnderGate = []string{"create_skill", "remove_skill"}

// repoRootFromPkgTools locates the repository root (this test lives in
// pkg/tools, two levels below the root).
func repoRootFromPkgTools(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	require.DirExists(t, filepath.Join(root, "pkg"), "repository root layout")
	return root
}

// goFilesUnder walks root and returns every non-test .go file path whose
// path is relative to repoRoot, sorted.
func goFilesUnder(t *testing.T, root, rel string) []string {
	t.Helper()
	dir := filepath.Join(root, rel)
	var out []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == "testdata" || name == "spa" || strings.HasPrefix(name, ".") && name != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			relPath, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			out = append(out, relPath)
		}
		return nil
	})
	require.NoError(t, err)
	sort.Strings(out)
	return out
}

// TestRetiredTools_GoneFromAutoApprove (§15 item 1).
// BDD: autoApproveClasses classifies neither retired name.
// Traces to: skills-spec-r15.md §15 item 1, T-E9b.
func TestRetiredTools_GoneFromAutoApprove(t *testing.T) {
	for _, name := range retiredToolNamesUnderGate {
		_, ok := autoApproveClasses[name]
		assert.False(t, ok, "%s must not carry an auto-approve classification (RED: it still does)", name)
	}
}

// TestRetiredTools_RefusalAnswersPresent (§15 item 1 allowed places + SC-002).
// BDD: the names appear ONLY in the allowed places this package owns — the
// refusal-answer map (retiredToolCanonicalNames) and T8's prune input
// (RetiredToolNames) — and both actually carry the two names.
// Traces to: skills-spec-r15.md §15 item 1, SC-002.
func TestRetiredTools_RefusalAnswersPresent(t *testing.T) {
	for _, name := range retiredToolNamesUnderGate {
		canonical, ok := retiredToolCanonicalNames[name]
		require.True(t, ok, "%s must have a refusal answer in retiredToolCanonicalNames (SC-002)", name)
		assert.NotEmpty(t, canonical, "the refusal answer names its canonical replacement")
	}
	require.NotNil(t, RetiredToolNames, "T8's prune input exists")
	for _, name := range retiredToolNamesUnderGate {
		assert.Contains(t, RetiredToolNames, name,
			"%s must be in RetiredToolNames so T8 prunes it from selectable fields", name)
	}
}

// TestRetiredTools_GoldenProviderDefs (§15 item 1).
// BDD: provider_defs.golden.json contains neither retired name.
// Traces to: skills-spec-r15.md §15 item 1, T-E9b.
func TestRetiredTools_GoldenProviderDefs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "provider_defs.golden.json"))
	require.NoError(t, err)
	body := string(raw)
	for _, name := range retiredToolNamesUnderGate {
		needle := `"` + name + `"`
		assert.NotContains(t, body, needle,
			"%s must be gone from provider_defs.golden.json (RED: it is still listed)", name)
	}
}

// TestRetiredTools_NoToolTypeImplementsRetiredName (§15 item 1).
// BDD: no type in pkg/ implements a tool whose Name() returns either name —
// an AST scan over non-test files, not a grep.
// Traces to: skills-spec-r15.md §15 item 1, T-E9b.
func TestRetiredTools_NoToolTypeImplementsRetiredName(t *testing.T) {
	root := repoRootFromPkgTools(t)
	needle := map[string]bool{"`create_skill`": true, "`remove_skill`": true,
		`"create_skill"`: true, `"remove_skill"`: true}

	var offenders []string
	for _, rel := range goFilesUnder(t, root, "pkg") {
		fset := token.NewFileSet()
		src, err := os.ReadFile(filepath.Join(root, rel))
		require.NoError(t, err)
		file, err := parser.ParseFile(fset, rel, src, 0)
		if err != nil {
			continue // generated or non-Go-parseable assets are not tool types
		}
		ast.Inspect(file, func(n ast.Node) bool {
			decl, ok := n.(*ast.FuncDecl)
			if !ok || decl.Name == nil || decl.Name.Name != "Name" || decl.Recv == nil {
				return true
			}
			ast.Inspect(decl.Body, func(m ast.Node) bool {
				ret, ok := m.(*ast.BasicLit)
				if ok && needle["`"+ret.Value+"`"] || ok && needle[ret.Value] {
					pos := fset.Position(ret.Pos())
					offenders = append(offenders, rel+":"+strconv.Itoa(pos.Line)+" returns "+ret.Value)
				}
				return true
			})
			return true
		})
	}
	assert.Empty(t, offenders,
		"no type in pkg/ may implement a tool named create_skill or remove_skill; found:\n%s",
		strings.Join(offenders, "\n"))
}

// TestRetiredTools_NoOriginMetaWriter (§15 item 2).
// BDD: writeOriginMeta is gone, and no non-test file writes
// .skill-origin.json through a file-write helper. (.skill-origin.json may
// appear only in contentRevision's skip list and the stamp writer — reads
// and comparisons, never creations.)
// Traces to: skills-spec-r15.md §15 item 2, T-E9b.
func TestRetiredTools_NoOriginMetaWriter(t *testing.T) {
	root := repoRootFromPkgTools(t)

	var symbolHits, writerHits []string
	writeHelpers := map[string]bool{
		"WriteFile": true, "WriteFileAtomic": true, "Rename": true,
		"Create": true, "Write": true,
	}
	for _, rel := range goFilesUnder(t, root, "pkg") {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		require.NoError(t, err)
		src := string(raw)
		if strings.Contains(src, "writeOriginMeta") {
			symbolHits = append(symbolHits, rel)
		}

		if !strings.Contains(src, ".skill-origin.json") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, rel, src, 0)
		if err != nil {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var fn string
			switch f := call.Fun.(type) {
			case *ast.Ident:
				fn = f.Name
			case *ast.SelectorExpr:
				fn = f.Sel.Name
			}
			if !writeHelpers[fn] {
				return true
			}
			// The written path's source text names the legacy metadata file.
			for _, arg := range call.Args {
				txt := exprSource(fset, src, arg)
				if strings.Contains(txt, ".skill-origin.json") {
					pos := fset.Position(call.Pos())
					writerHits = append(writerHits, rel+":"+strconv.Itoa(pos.Line)+" "+fn)
					break
				}
			}
			return true
		})
	}
	assert.Empty(t, symbolHits,
		"writeOriginMeta must be gone from production code (RED: still live in %s)",
		strings.Join(symbolHits, ", "))
	assert.Empty(t, writerHits,
		"no production code path may create .skill-origin.json; found:\n%s",
		strings.Join(writerHits, "\n"))
}

// exprSource extracts an expression's source text (single-line slice of src).
func exprSource(fset *token.FileSet, src string, e ast.Expr) string {
	startPos, endPos := e.Pos(), e.End()
	if !startPos.IsValid() || !endPos.IsValid() {
		return ""
	}
	start, end := fset.Position(startPos), fset.Position(endPos)
	lines := strings.Split(src, "\n")
	if start.Line-1 < 0 || start.Line-1 >= len(lines) {
		return ""
	}
	line := lines[start.Line-1]
	col := start.Column - 1
	if end.Line != start.Line {
		return line[col:]
	}
	return line[col : col+(end.Column-start.Column)]
}

// textGateBannedSymbols are §15 item 3's gone-forever names.
var textGateBannedSymbols = []string{
	"OMNIPUS_DOCUMENT_SKILLS",
	"InstallEmbeddedSkills",
	"SeedDefaults",
	"deleteOrphanedDefineDoneDir",
	"RefreshUnmodifiedDefaults",
	"PublishStagedSkill",
	"parseSimpleYAML",
}

// textGateMatches reports the banned-symbol matches in src using
// symbol-boundary (\b…) semantics: an identifier token equal to a banned
// name matches; a longer identifier that merely CONTAINS a banned name as a
// substring (WorkspaceSeedDefaultsConfig, defaultWorkspaceSeedDefaults,
// SelfEdgeSeedDefaults, config.WorkspaceSeedDefaults) does not.
func textGateMatches(src string) []string {
	tokenRe := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	banned := map[string]bool{}
	for _, s := range textGateBannedSymbols {
		banned[s] = true
	}
	var hits []string
	seen := map[string]bool{}
	for _, tok := range tokenRe.FindAllString(src, -1) {
		if banned[tok] && !seen[tok] {
			seen[tok] = true
			hits = append(hits, tok)
		}
	}
	return hits
}

// TestRetiredTools_TextGate (§15 item 3).
// BDD: the symbol-boundary text gate over pkg/ and cmd/ finds none of the
// seven gone-forever symbols outside _test.go files.
// Traces to: skills-spec-r15.md §15 item 3, SC-002, row 4.
func TestRetiredTools_TextGate(t *testing.T) {
	root := repoRootFromPkgTools(t)
	var offenders []string
	for _, rel := range append(goFilesUnder(t, root, "pkg"), goFilesUnder(t, root, "cmd")...) {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		require.NoError(t, err)
		for _, hit := range textGateMatches(string(raw)) {
			offenders = append(offenders, rel+": "+hit)
		}
	}
	assert.Empty(t, offenders,
		"§15 item 3 text gate: gone-forever symbols must not appear outside tests; found:\n%s",
		strings.Join(offenders, "\n"))
}

// TestRetiredTools_TextGateFeasibility (§15 item 3, r3).
// BDD: the gate REJECTS a real remaining skills symbol (a leftover
// `func SeedDefaults` must fail it) and ACCEPTS each of the four unrelated
// live workspace identifiers unchanged — passing the gate never requires
// renaming an unrelated API. The four identifiers are additionally verified
// to still exist in the real sources, so the acceptance is not vacuous.
// Traces to: skills-spec-r15.md §15 item 3 (r3 feasibility), row 4.
func TestRetiredTools_TextGateFeasibility(t *testing.T) {
	t.Run("rejects a leftover skills symbol", func(t *testing.T) {
		leftover := "package skills\n\n// leftover\nfunc SeedDefaults() {}\n"
		hits := textGateMatches(leftover)
		assert.Contains(t, hits, "SeedDefaults",
			"the gate must reject a leftover skills SeedDefaults")
	})

	t.Run("accepts the four workspace identifiers", func(t *testing.T) {
		for _, ident := range []string{
			"WorkspaceSeedDefaultsConfig",
			"SelfEdgeSeedDefaults",
			"defaultWorkspaceSeedDefaults",
			"config.WorkspaceSeedDefaults",
		} {
			assert.Empty(t, textGateMatches(ident),
				"%s must pass the gate unchanged (substring containment is not a match)", ident)
		}
	})

	t.Run("the four identifiers still exist in the real sources", func(t *testing.T) {
		root := repoRootFromPkgTools(t)
		realFiles := []string{
			filepath.Join("pkg", "config", "workspace_seed_defaults.go"),
			filepath.Join("pkg", "config", "config.go"),
			filepath.Join("pkg", "gateway", "blocked_paths.go"),
		}
		var corpus string
		for _, rel := range realFiles {
			raw, err := os.ReadFile(filepath.Join(root, rel))
			require.NoError(t, err, "the pinned identifier file must still exist")
			corpus += string(raw)
		}
		for _, ident := range []string{
			"WorkspaceSeedDefaultsConfig", "SelfEdgeSeedDefaults", "defaultWorkspaceSeedDefaults",
		} {
			assert.Contains(t, corpus, ident,
				"the live identifier %s is still present, so accepting it is not vacuous", ident)
		}
	})
}
