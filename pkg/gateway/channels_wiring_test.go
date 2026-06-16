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
// After the Spec-2 map migration, initChannels uses a data-driven loop (no
// hardcoded initChannel("name", ...) string literals). The wiring guarantee is
// now expressed differently: every channel sub-package that calls
// channels.RegisterFactory(name, ...) in its init() must be blank-imported
// somewhere in pkg/gateway/ so that its init() runs at startup.
//
// This test asserts source-level wiring intent rather than runtime
// registration so it stays correct under any build-tag combination:
//
//  1. parse every *.go file under pkg/channels/<subpackage>/ (excluding test
//     files) and collect every RegisterFactory("name", ...) call's first
//     string argument — these are the factory names that NEED blank imports;
//  2. parse every *.go file under pkg/gateway/ (regardless of build tags) and
//     collect every blank import of pkg/channels/<subpackage>;
//  3. assert each factory name maps to at least one blank import.
//
// A channel may legitimately be guarded by a build tag (matrix's CGO+OS
// constraints are an example) — what is forbidden is calling RegisterFactory
// from a sub-package without any companion blank import that would let its
// init() run on at least one supported platform.
func TestNoHalfWiredChannels(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	channelsDir := filepath.Join(repoRoot, "pkg", "channels")

	// registeredFactories maps factory name → sub-package directory name
	// (e.g. "google-chat" → "googlechat", "telegram" → "telegram").
	registeredFactories := collectRegisteredFactories(t, channelsDir)
	if len(registeredFactories) == 0 {
		t.Fatal("no RegisterFactory(\"name\", ...) calls found under pkg/channels/ — " +
			"AST parse drifted or all channel packages were removed; fix the test")
	}

	// subpackageOverrides reverses the factory-name → subdir mapping for factories
	// whose name differs from their sub-package directory name.
	// Key: factory name, value: sub-package dir name.
	// (googlechat's factory is "google-chat" but the dir is "googlechat".)
	// This map is the inverse of what initChannelArguments previously used.
	// We build it from the discovered registeredFactories map directly.

	imported := blankImportedChannels(t, filepath.Join(repoRoot, "pkg", "gateway"))

	var halfWired []string
	for factoryName, subpkg := range registeredFactories {
		if _, ok := imported[subpkg]; !ok {
			halfWired = append(halfWired,
				"factory \""+factoryName+"\" (pkg/channels/"+subpkg+") has no blank import in pkg/gateway/")
		}
	}
	sort.Strings(halfWired)

	if len(halfWired) > 0 {
		t.Fatalf(
			"half-wired channels — RegisterFactory called in sub-package"+
				" but no companion blank import in pkg/gateway/*.go:\n  %v\n\n"+
				"Either blank-import the channel subpackage somewhere under pkg/gateway/"+
				" (so its init() can run and call channels.RegisterFactory),"+
				" or remove the RegisterFactory call entirely.",
			strings.Join(halfWired, "\n  "),
		)
	}
}

// collectRegisteredFactories walks pkg/channels/<subpackage>/*.go (non-test
// files) and collects every RegisterFactory("name", ...) call's first string
// argument. Returns a map of factoryName → subpackage directory name.
func collectRegisteredFactories(t *testing.T, channelsDir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(channelsDir)
	if err != nil {
		t.Fatalf("read channels dir: %v", err)
	}
	result := map[string]string{}
	fset := token.NewFileSet()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		subpkg := e.Name()
		subDir := filepath.Join(channelsDir, subpkg)
		goFiles, err := os.ReadDir(subDir)
		if err != nil {
			t.Logf("warn: could not read %s: %v", subDir, err)
			continue
		}
		for _, gf := range goFiles {
			if gf.IsDir() || !strings.HasSuffix(gf.Name(), ".go") ||
				strings.HasSuffix(gf.Name(), "_test.go") {
				continue
			}
			filePath := filepath.Join(subDir, gf.Name())
			file, err := parser.ParseFile(fset, filePath, nil, parser.SkipObjectResolution)
			if err != nil {
				// Parse errors under build-tag-excluded files are expected; skip.
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel == nil || sel.Sel.Name != "RegisterFactory" {
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
				if _, seen := result[name]; !seen {
					result[name] = subpkg
				}
				return true
			})
		}
	}
	return result
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
