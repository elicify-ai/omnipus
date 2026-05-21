package gateway

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestNoHalfWiredChannels is the regression guard for issue #161.
//
// Before the fix, pkg/channels/teams existed and was referenced from
// Manager.initChannels via initChannel("teams", "Microsoft Teams"), but the
// teams package was never blank-imported anywhere in production code. The
// init() that calls channels.RegisterFactory("teams", ...) therefore never
// ran, the factory lookup always failed at runtime, and the operator-visible
// `channels.teams.enabled` config flag was a dead knob.
//
// This test asserts source-level wiring intent rather than runtime
// registration so it stays correct under any build-tag combination:
//
//  1. parse pkg/channels/manager.go and collect every initChannel("name", ...)
//     call's first argument (the channel subpackage name);
//  2. parse every *.go file under pkg/gateway/ (regardless of build tags) and
//     collect every blank import of pkg/channels/<subpackage>;
//  3. assert each initChannel name maps to at least one blank import.
//
// A channel may legitimately be guarded by a build tag (matrix's CGO+OS
// constraints are an example) — what is forbidden is referencing the channel
// from initChannel without any companion blank import that would let its
// factory register at startup on at least one supported platform.
func TestNoHalfWiredChannels(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	wantedNames := initChannelArguments(t, filepath.Join(repoRoot, "pkg", "channels", "manager.go"))
	if len(wantedNames) == 0 {
		t.Fatal("no initChannel(\"name\", ...) calls found in manager.go — AST parse drifted, fix the test")
	}

	imported := blankImportedChannels(t, filepath.Join(repoRoot, "pkg", "gateway"))

	subpackageOverrides := map[string]string{
		"google-chat": "googlechat",
	}

	var halfWired []string
	for name, pos := range wantedNames {
		pkg := name
		if override, ok := subpackageOverrides[name]; ok {
			pkg = override
		}
		if _, ok := imported[pkg]; !ok {
			halfWired = append(halfWired, name+" -> pkg/channels/"+pkg+" ("+pos.String()+")")
		}
	}
	sort.Strings(halfWired)

	if len(halfWired) > 0 {
		t.Fatalf("half-wired channels — referenced by initChannel(...) in manager.go but no companion blank import in pkg/gateway/*.go:\n  %v\n\nEither blank-import the channel subpackage somewhere under pkg/gateway/ (so its init() can run and call channels.RegisterFactory), or delete the initChannel branch entirely.", halfWired)
	}
}

func initChannelArguments(t *testing.T, managerPath string) map[string]token.Position {
	t.Helper()
	if _, err := os.Stat(managerPath); err != nil {
		t.Fatalf("manager.go not found at %s: %v", managerPath, err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, managerPath, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse manager.go: %v", err)
	}
	out := map[string]token.Position{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil || sel.Sel.Name != "initChannel" {
			return true
		}
		if len(call.Args) == 0 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		name, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		if _, seen := out[name]; !seen {
			out[name] = fset.Position(lit.Pos())
		}
		return true
	})
	return out
}

func blankImportedChannels(t *testing.T, gatewayDir string) map[string]struct{} {
	t.Helper()
	out := map[string]struct{}{}
	entries, err := os.ReadDir(gatewayDir)
	if err != nil {
		t.Fatalf("read gateway dir: %v", err)
	}
	const channelPrefix = "github.com/dapicom-ai/omnipus/pkg/channels/"
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		path := filepath.Join(gatewayDir, e.Name())
		// parser.ParseFile ignores build constraints, which is what we want
		// here: we are checking source-level wiring intent, not what is
		// compiled into the current binary.
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range file.Imports {
			if imp.Name == nil || imp.Name.Name != "_" {
				continue
			}
			pathLit, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			if !strings.HasPrefix(pathLit, channelPrefix) {
				continue
			}
			sub := strings.TrimPrefix(pathLit, channelPrefix)
			out[sub] = struct{}{}
		}
	}
	return out
}
