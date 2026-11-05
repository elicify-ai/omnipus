package agent

// T-17 / FR-043 / S-43: supplementary producer drift guard. The inventory is
// the spec's three producers, keyed by complete repo path + enclosing function,
// with one literal each. A new producer in an already-approved file/function
// must fail too. Dispatch/classification behavior is separately T-12/T-15/T-16.
// The Go AST scanner handles inline, nested and elided collection literals,
// import aliases and comments. Instrument tests prove it detects additions and
// rejects incomplete scans; it never treats a read/parse error as an empty file.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type t17Producer struct {
	path, owner string
	line        int
}

func t17InventoryIssues(producers []t17Producer) []string {
	// Independent oracle: FR-043's producer table, not a discovered allowlist.
	inventory := map[string]string{
		"pkg/agent/loop_wire.go::registerCoreTools":             "send_message callback: copies checked/start from the tool",
		"pkg/agent/address_router.go::replyViaConnector":        "captured return: carries Return",
		"pkg/agent/goal_record_wiring.go::afterGoalRecordWrite": "goal echo: empty Operation, existing dispatch ownership check",
	}
	seen := map[string]int{}
	var issues []string
	for _, p := range producers {
		key := p.path + "::" + p.owner
		seen[key]++
		if _, ok := inventory[key]; !ok || seen[key] > 1 {
			issues = append(issues, fmt.Sprintf("%s:%d: classify this producer in FR-043 (S-43)", key, p.line))
		}
	}
	for key, class := range inventory {
		if seen[key] == 0 {
			issues = append(issues, fmt.Sprintf("FR-043 producer %s (%s) disappeared; reconcile the spec inventory deliberately", key, class))
		}
	}
	return issues
}

func TestT17_ProducerInventory_EveryAgentIDOutboundIsClassified_S43(t *testing.T) {
	producers, err := t17ScanAgentIDOutboundLiterals(findRepoRoot(t))
	require.NoError(t, err, "instrument: the complete source scan must succeed")
	require.NotEmpty(t, producers, "instrument: source scan must find the pinned producers")
	require.Empty(t, t17InventoryIssues(producers), "classify this producer in FR-043")
	require.Len(t, producers, 3, "FR-043 pins exactly three AgentID-bearing producers")
}

func t17ScanAgentIDOutboundLiterals(repoRoot string) ([]t17Producer, error) {
	var out []t17Producer
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", "node_modules", "dist", ".git", "testdata", "spa":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		producers, err := t17ParseProducers(filepath.ToSlash(rel), path)
		if err != nil {
			return err
		}
		out = append(out, producers...)
		return nil
	})
	return out, err
}

func t17ParseProducers(rel, path string) ([]t17Producer, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", rel, err)
	}
	alias := ""
	for _, imp := range file.Imports {
		importPath, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		if importPath == "github.com/elicify-ai/omnipus/pkg/bus" {
			alias = "bus"
			if imp.Name != nil {
				alias = imp.Name.Name
			}
		}
	}
	if alias == "" && file.Name.Name != "bus" {
		return nil, nil
	}
	var out []t17Producer
	inferred := map[*ast.CompositeLit]ast.Expr{}
	for _, decl := range file.Decls {
		owner := "<package>"
		if fn, ok := decl.(*ast.FuncDecl); ok {
			owner = fn.Name.Name
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			typ := lit.Type
			if typ == nil {
				typ = inferred[lit]
			}
			t17InferCollectionElements(lit, typ, inferred)
			if t17IsOutboundType(typ, alias) && t17HasAgentID(lit) {
				out = append(out, t17Producer{path: rel, owner: owner, line: fset.Position(lit.Pos()).Line})
			}
			return true
		})
	}
	return out, nil
}

func t17InferCollectionElements(lit *ast.CompositeLit, typ ast.Expr, inferred map[*ast.CompositeLit]ast.Expr) {
	var elem ast.Expr
	switch collection := typ.(type) {
	case *ast.ArrayType:
		elem = collection.Elt
	case *ast.MapType:
		elem = collection.Value
	default:
		return
	}
	for _, value := range lit.Elts {
		if keyed, ok := value.(*ast.KeyValueExpr); ok {
			value = keyed.Value
		}
		if child, ok := value.(*ast.CompositeLit); ok && child.Type == nil {
			inferred[child] = elem
		}
	}
}

func t17IsOutboundType(typ ast.Expr, alias string) bool {
	if ptr, ok := typ.(*ast.StarExpr); ok {
		return t17IsOutboundType(ptr.X, alias)
	}
	if ident, ok := typ.(*ast.Ident); ok {
		return (alias == "." || alias == "") && ident.Name == "OutboundMessage"
	}
	selector, ok := typ.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "OutboundMessage" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == alias
}

func t17HasAgentID(lit *ast.CompositeLit) bool {
	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "AgentID" {
				return true
			}
		}
	}
	return false
}

func TestT17_ScannerInstrument_InlineNestedAliasAndElidedLiterals(t *testing.T) {
	root := t.TempDir()
	src := `package fixture
import msg "github.com/elicify-ai/omnipus/pkg/bus"
// bus.OutboundMessage{AgentID: "comment"} must not count.
func newProducer() {
 _ = msg.OutboundMessage{AgentID: "inline"}
 _ = struct{ Message any }{Message: msg.OutboundMessage{Content: "nested", AgentID: "nested"}}
 _ = []msg.OutboundMessage{{AgentID: "elided"}}
 _ = map[string]*msg.OutboundMessage{"k": {AgentID: "map-elided"}}
 _ = msg.OutboundMessage{Content: "system without AgentID"}
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "extra.go"), []byte(src), 0o600))
	producers, err := t17ScanAgentIDOutboundLiterals(root)
	require.NoError(t, err)
	require.Len(t, producers, 4, "instrument catches inline, nested and both elided forms, not comments/system messages")
	for _, p := range producers {
		require.Equal(t, "extra.go", p.path)
		require.Equal(t, "newProducer", p.owner)
	}
	require.Contains(t, strings.Join(t17InventoryIssues(producers), "\n"), "classify this producer in FR-043")
}

func TestT17_InventoryInstrument_NewProducerInApprovedFileIsNotWhitelisted(t *testing.T) {
	base := []t17Producer{
		{path: "pkg/agent/loop_wire.go", owner: "registerCoreTools"},
		{path: "pkg/agent/address_router.go", owner: "replyViaConnector"},
		{path: "pkg/agent/goal_record_wiring.go", owner: "afterGoalRecordWrite"},
	}
	require.Empty(t, t17InventoryIssues(base), "instrument: the spec table itself is accepted")
	for _, owner := range []string{"newProducer", "registerCoreTools"} {
		t.Run(owner, func(t *testing.T) {
			probe := append([]t17Producer{}, base...)
			probe = append(probe, t17Producer{path: "pkg/agent/loop_wire.go", owner: owner, line: 99})
			issues := t17InventoryIssues(probe)
			require.Len(t, issues, 1, "a new function OR second literal in an approved function must be classified")
			require.Contains(t, issues[0], "classify this producer in FR-043")
		})
	}
}

func TestT17_ScannerInstrument_SyntaxErrorCannotProduceAnEmptyGreen(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "broken.go"), []byte("package broken\nfunc {"), 0o600))
	_, err := t17ScanAgentIDOutboundLiterals(root)
	require.ErrorContains(t, err, "scan broken.go:", "an incomplete scan must fail instead of silently omitting a source file")
}
