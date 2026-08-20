// Command dead-exports-scan is the analysis engine behind
// scripts/check-dead-exports.sh (see that file for the full rationale).
//
// It finds exported (capitalized) top-level funcs, methods, and named types
// that have ZERO references anywhere in production Go code (non-test files,
// production build tags only).
//
// # WHY NOT golangci-lint's `unused` LINTER
//
// staticcheck's unused analysis (wrapped by golangci-lint's `unused` linter)
// deliberately does NOT flag exported package-level identifiers by default:
// it treats them as potentially-public API, because for an importable
// library there is no way to statically know whether an external module
// calls them. This repo compiles to a single binary (CLAUDE.md constraint
// #2) with no external importers, so that caveat does not apply here — but
// golangci-lint v2.12.2's `unused` linter settings (checked against its
// published JSON schema — `unusedSettings` only exposes
// field-writes-are-uses, post-statements-are-reads, exported-fields-are-used,
// parameters-are-used, local-variables-are-used, generated-is-used) expose no
// "exported funcs/methods/types are used" toggle to opt back into flagging
// exported symbols. The setting genuinely does not exist in this version;
// this is not a guess.
//
// # WHY NOT A SHELL GREP
//
// grep is not type-aware: `\.CloseSession(` matches both
// `BrowserManager.CloseSession(sessionID string)` and the unrelated
// `agentLoop.CloseSession(sessionID, "idle")` (two different types, two
// different signatures, same method name). This tool resolves every
// identifier through go/types, so it only ever counts a REAL reference to
// the SAME types.Object as a "use" — never a name collision.
//
// WHY NOT golang.org/x/tools/go/packages
//
// That package is the obvious library for this (it's what gopls/staticcheck
// use internally), but it is not a dependency of this module: go.mod has no
// require entry for golang.org/x/tools, and adding one requires `go mod
// tidy`, which on this tree also bumps unrelated transitive dependencies
// (golang.org/x/crypto, golang.org/x/net, golang.org/x/term, and promotes
// two github.com/pion/* packages from indirect to direct) — measured
// directly, not assumed. Touching those to add a lint script is out of
// scope and out of this agent's remit ("do not modify Go source" / no go.mod
// churn for unrelated deps). This tool instead uses ONLY the standard
// library: go/parser + go/types + go/importer's "source" mode, driven by
// `go list -json` (a subprocess call to the go tool, not a module
// dependency) for package discovery. Verified: go/importer's "source" mode
// resolves both same-module packages and third-party module-cache
// dependencies (github.com/oklog/ulid/v2, github.com/chromedp/chromedp,
// golang.org/x/crypto/argon2 all resolved cleanly in a standalone test)
// without any go.mod change.
//
// HOW IT WORKS
//
//  1. Run `go list -json <tags>' ./...` to enumerate every package in the
//     module together with its production (non-test) Go files — `go list`
//     already excludes _test.go files and files disabled by build
//     constraints for the given tags; this tool never re-implements that
//     logic.
//  2. Type-check every one of those packages with a SHARED go/types
//     Importer (this program's own modImporter), memoized by import path, so
//     that a types.Object for a symbol declared in package B is the exact
//     same pointer whether reached by iterating packages directly or by
//     resolving an import from package A. Object pointer identity is what
//     lets this tool tell "the browser package's CloseSession" apart from
//     "the agent package's CloseSession" even though grep cannot.
//     Non-module imports (stdlib, third-party) are delegated to
//     go/importer's "source" mode.
//  3. Walk every module package's top-level declarations. Any exported
//     (Name.IsExported()) func, method, or named type declared in a
//     non-generated file is a CANDIDATE.
//  4. Walk every identifier (including every selector's Sel field, which
//     covers method calls and qualified type references) in every module
//     package's files and resolve it via types.Info.Uses. Record the
//     referenced types.Object in a "used" set, keyed by pointer identity.
//     Because test files are never part of the `go list` production file
//     set from step 1, a reference that exists ONLY in a _test.go file
//     (e.g. BrowserManager.CloseSession, whose only caller is
//     reaper_lifecycle_test.go) never enters this set.
//  5. Any candidate whose Object never appears in the "used" set is dead:
//     nothing anywhere in the production build graph ever refers to it.
//
// KNOWN LIMITATION (documented, not hidden)
//
// This is reference counting, not call-graph reachability: if an exported
// method is called only through an interface value (dynamic dispatch), the
// identifier at the call site resolves to the INTERFACE method's Object, not
// the concrete type's Object, so a concrete implementation can be flagged as
// "dead" even though it is reachable at runtime through that interface. This
// is a real false-positive class — see scripts/dead-exports-allowlist.txt
// for entries justified on exactly this basis, seeded only where verified
// against the codebase, never to force a clean run.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// listPkg mirrors the subset of `go list -json` output this tool needs.
type listPkg struct {
	ImportPath string   `json:"ImportPath"`
	Dir        string   `json:"Dir"`
	GoFiles    []string `json:"GoFiles"` // production (non-test) .go files matching build constraints
	Imports    []string `json:"Imports"`
}

type candidate struct {
	qualified string // "Type.Method" or "FuncName" or "TypeName"
	kind      string // "func" | "method" | "type"
	pos       token.Position
	obj       types.Object
	file      string // absolute path, for file-level allowlist matching
}

// modImporter type-checks this module's own packages itself (so every
// package it resolves shares one token.FileSet and one Object identity
// space) and delegates everything outside the module to go/importer's
// "source" mode.
type modImporter struct {
	fset       *token.FileSet
	listInfo   map[string]listPkg
	fallback   types.Importer
	cache      map[string]*types.Package
	inProgress map[string]bool

	// Retained per module package for the candidate/use walk.
	files map[string][]*ast.File
	info  map[string]*types.Info
}

func newModImporter(fset *token.FileSet, listInfo map[string]listPkg, tags []string) *modImporter {
	// go/importer's "source" mode resolves third-party/stdlib imports through
	// go/build.Default. It has no per-call way to pass custom build tags, so
	// this sets them on the shared go/build.Default context before
	// constructing the importer — the same effect as `go build -tags=...`
	// for every package this tool does NOT type-check itself (i.e.
	// everything outside this module). Without this, packages gated behind
	// this repo's own "goolm"/"stdjson" tags (e.g. maunium.net/go/mautrix's
	// cgo-only libolm backend, which this repo's "goolm" tag exists
	// specifically to route around) fail to resolve.
	build.Default.BuildTags = tags
	return &modImporter{
		fset:       fset,
		listInfo:   listInfo,
		fallback:   importer.ForCompiler(fset, "source", nil),
		cache:      map[string]*types.Package{},
		inProgress: map[string]bool{},
		files:      map[string][]*ast.File{},
		info:       map[string]*types.Info{},
	}
}

func (mi *modImporter) Import(path string) (*types.Package, error) {
	if pkg, ok := mi.cache[path]; ok {
		return pkg, nil
	}
	lp, ok := mi.listInfo[path]
	if !ok {
		// Not part of this module's own package set — stdlib or a
		// third-party dependency. Delegate to the standard "source"
		// importer, which resolves module-cache and stdlib packages alike.
		pkg, err := mi.fallback.Import(path)
		if err != nil {
			return nil, fmt.Errorf("importing external package %q: %w", path, err)
		}
		mi.cache[path] = pkg
		return pkg, nil
	}
	if mi.inProgress[path] {
		return nil, fmt.Errorf("import cycle detected at %q", path)
	}
	mi.inProgress[path] = true
	defer delete(mi.inProgress, path)

	var files []*ast.File
	for _, name := range lp.GoFiles {
		full := filepath.Join(lp.Dir, name)
		f, err := parser.ParseFile(mi.fset, full, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", full, err)
		}
		files = append(files, f)
	}

	info := &types.Info{
		Defs: map[*ast.Ident]types.Object{},
		Uses: map[*ast.Ident]types.Object{},
	}
	conf := types.Config{Importer: mi, Error: func(err error) {
		fmt.Fprintf(os.Stderr, "dead-exports-scan: type-check warning in %s: %v\n", path, err)
	}}
	pkg, checkErr := conf.Check(path, mi.fset, files, info)
	// A package that reports non-fatal errors via conf.Error but still
	// returns a non-nil *types.Package has partial-but-usable info; only
	// abort on a totally failed check (pkg == nil).
	if pkg == nil {
		return nil, fmt.Errorf("type-checking %s: %w", path, checkErr)
	}

	mi.cache[path] = pkg
	mi.files[path] = files
	mi.info[path] = info
	return pkg, nil
}

func main() {
	tags := "goolm,stdjson"
	if v := os.Getenv("DEAD_EXPORTS_TAGS"); v != "" {
		tags = v
	}

	listInfo, order, err := goList(tags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dead-exports-scan: %v\n", err)
		os.Exit(2)
	}
	if len(listInfo) == 0 {
		fmt.Fprintln(os.Stderr, "dead-exports-scan: `go list` returned zero packages — wrong cwd or empty module")
		os.Exit(2)
	}

	fset := token.NewFileSet()
	mi := newModImporter(fset, listInfo, strings.Split(tags, ","))

	failed := 0
	for _, path := range order {
		if _, err := mi.Import(path); err != nil {
			fmt.Fprintf(os.Stderr, "dead-exports-scan: failed to load %s: %v\n", path, err)
			failed++
		}
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "dead-exports-scan: %d package(s) failed to load; refusing to report a verdict against a partially-loaded graph\n", failed)
		os.Exit(2)
	}

	used := map[types.Object]bool{}
	var candidates []candidate
	var allInterfaces []*types.Named // every named interface type declared anywhere in the module, exported or not

	for _, path := range order {
		info := mi.info[path]
		for _, file := range mi.files[path] {
			generated := isGenerated(file)

			// Pass 1: collect exported top-level declarations from this file.
			if !generated {
				for _, decl := range file.Decls {
					switch d := decl.(type) {
					case *ast.FuncDecl:
						if !d.Name.IsExported() {
							continue
						}
						obj := info.Defs[d.Name]
						if obj == nil {
							continue
						}
						qualified := d.Name.Name
						kind := "func"
						if d.Recv != nil && len(d.Recv.List) > 0 {
							kind = "method"
							qualified = receiverTypeName(d.Recv.List[0].Type) + "." + d.Name.Name
						}
						candidates = append(candidates, candidate{
							qualified: qualified,
							kind:      kind,
							pos:       fset.Position(d.Name.Pos()),
							obj:       obj,
							file:      fset.Position(d.Name.Pos()).Filename,
						})
					case *ast.GenDecl:
						if d.Tok != token.TYPE {
							continue
						}
						for _, spec := range d.Specs {
							ts, ok := spec.(*ast.TypeSpec)
							if !ok {
								continue
							}
							obj := info.Defs[ts.Name]
							if obj == nil {
								continue
							}
							if tn, ok := obj.(*types.TypeName); ok {
								if named, ok := tn.Type().(*types.Named); ok {
									if _, isIface := named.Underlying().(*types.Interface); isIface {
										allInterfaces = append(allInterfaces, named)
									}
								}
							}
							if !ts.Name.IsExported() {
								continue
							}
							candidates = append(candidates, candidate{
								qualified: ts.Name.Name,
								kind:      "type",
								pos:       fset.Position(ts.Name.Pos()),
								obj:       obj,
								file:      fset.Position(ts.Name.Pos()).Filename,
							})
						}
					}
				}
			}

			// Pass 2: every identifier reference in this file (generated or
			// not — a generated file calling into hand-written code is a
			// perfectly real production use) counts toward "used".
			ast.Inspect(file, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					if obj := info.Uses[id]; obj != nil {
						used[obj] = true
					}
				}
				return true
			})
		}
	}

	// Interface-satisfaction false-positive mitigation (documented KNOWN
	// LIMITATION above): a method reached only through dynamic dispatch on
	// an interface value resolves, at its call site, to the INTERFACE
	// method's Object — never the concrete type's Object — so plain
	// reference counting cannot see it. Recover the common case: if a
	// candidate method's receiver type structurally implements some
	// interface that is itself actually referenced somewhere in production
	// code (i.e. that interface's own type name is in the "used" set, not
	// just declared and ignored), and that interface's method set includes
	// this exact method name, treat the method as reachable through that
	// interface. This is intentionally conservative in the safe direction:
	// it can under-flag (miss a genuinely dead method that happens to share
	// a name+signature with a live interface's method on the same type) but
	// it will not over-flag production code as dead.
	liveInterfaces := make([]*types.Named, 0, len(allInterfaces))
	for _, iface := range allInterfaces {
		if used[iface.Obj()] {
			liveInterfaces = append(liveInterfaces, iface)
		}
	}

	satisfiesLiveInterface := func(c candidate) bool {
		if c.kind != "method" {
			return false
		}
		fn, ok := c.obj.(*types.Func)
		if !ok {
			return false
		}
		sig, ok := fn.Type().(*types.Signature)
		if !ok || sig.Recv() == nil {
			return false
		}
		recvType := sig.Recv().Type()
		named, ok := recvType.(*types.Named)
		if !ok {
			if ptr, ok := recvType.(*types.Pointer); ok {
				named, ok = ptr.Elem().(*types.Named)
				if !ok {
					return false
				}
			} else {
				return false
			}
		}
		for _, iface := range liveInterfaces {
			ifaceType, ok := iface.Underlying().(*types.Interface)
			if !ok {
				continue
			}
			hasMethod := false
			for i := 0; i < ifaceType.NumMethods(); i++ {
				if ifaceType.Method(i).Name() == fn.Name() {
					hasMethod = true
					break
				}
			}
			if !hasMethod {
				continue
			}
			if types.Implements(named, ifaceType) || types.Implements(types.NewPointer(named), ifaceType) {
				return true
			}
		}
		return false
	}

	allow, allowFiles := loadAllowlist("scripts/dead-exports-allowlist.txt")

	var dead []candidate
	var suppressedByInterface int
	for _, c := range candidates {
		if used[c.obj] {
			continue
		}
		if allow[c.qualified] {
			continue
		}
		if relFile(c.file) != "" && allowFiles[relFile(c.file)] {
			continue
		}
		if satisfiesLiveInterface(c) {
			suppressedByInterface++
			continue
		}
		dead = append(dead, c)
	}

	sort.Slice(dead, func(i, j int) bool {
		if dead[i].pos.Filename != dead[j].pos.Filename {
			return dead[i].pos.Filename < dead[j].pos.Filename
		}
		return dead[i].pos.Line < dead[j].pos.Line
	})

	for _, c := range dead {
		fmt.Printf("%s:%d: dead export: %s %s\n", relFile(c.pos.Filename), c.pos.Line, c.kind, c.qualified)
	}

	fmt.Fprintf(os.Stderr, "dead-exports-scan: %d candidate(s) scanned across %d package(s), %d dead, "+
		"%d suppressed (satisfies a live interface), %d allowlisted (%d by name, %d by file)\n",
		len(candidates), len(order), len(dead), suppressedByInterface,
		len(allow)+len(allowFiles), len(allow), len(allowFiles))

	if len(dead) > 0 {
		os.Exit(1)
	}
}

// goList runs `go list -json` over ./... with the given build tags and
// returns every package keyed by import path, plus a deterministic
// (sorted) iteration order.
func goList(tags string) (map[string]listPkg, []string, error) {
	args := []string{"list", "-json", "./..."}
	if tags != "" {
		args = []string{"list", "-json", "-tags=" + tags, "./..."}
	}
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("go list: creating stdout pipe: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("go list: starting: %w", err)
	}

	result := map[string]listPkg{}
	dec := json.NewDecoder(bufio.NewReaderSize(stdout, 1<<20))
	for {
		var lp listPkg
		if err := dec.Decode(&lp); err != nil {
			if err.Error() == "EOF" {
				break
			}
			_ = cmd.Wait()
			return nil, nil, fmt.Errorf("go list: decoding output: %w", err)
		}
		// `go list ./...` has no built-in concept of npm's node_modules/ (only
		// vendor/ is special-cased by the go tool) — a stray vendored Go
		// mirror shipped inside a JS dependency (observed:
		// node_modules/flatted/golang/pkg/flatted) is otherwise picked up as
		// if it were this project's own code. Exclude any package whose
		// directory passes through a node_modules/ segment; it is not code
		// this project's engineers own or watch for dead exports.
		if strings.Contains(lp.Dir, string(filepath.Separator)+"node_modules"+string(filepath.Separator)) {
			continue
		}
		result[lp.ImportPath] = lp
	}
	if err := cmd.Wait(); err != nil {
		return nil, nil, fmt.Errorf("go list: %w (stderr: %s)", err, stderr.String())
	}

	order := make([]string, 0, len(result))
	for path := range result {
		order = append(order, path)
	}
	sort.Strings(order)
	return result, order, nil
}

// receiverTypeName extracts "Foo" from receiver type expressions "Foo" or
// "*Foo" (the only two forms Go allows for a method receiver).
func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.IndexExpr: // generic receiver Foo[T]
		return receiverTypeName(t.X)
	case *ast.IndexListExpr: // generic receiver Foo[T, U]
		return receiverTypeName(t.X)
	default:
		return "?"
	}
}

// isGenerated matches the standard machine-generated-file marker
// (https://go.dev/s/generatedcode): a comment line matching
// `^// Code generated .* DO NOT EDIT\.$` anywhere before the package clause.
func isGenerated(file *ast.File) bool {
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			text := c.Text
			if strings.HasPrefix(text, "// Code generated ") && strings.HasSuffix(text, " DO NOT EDIT.") {
				return true
			}
		}
		if cg.Pos() > file.Package {
			break
		}
	}
	return false
}

// loadAllowlist reads scripts/dead-exports-allowlist.txt: one
// "Qualified.Name  # justification" entry per line. Blank lines and lines
// starting with '#' are ignored. Missing file is not an error (empty
// allowlist).
// loadAllowlist parses scripts/dead-exports-allowlist.txt. Two entry forms:
//
//	Qualified.Name         # justification
//	FILE:relative/path.go  # justification — every export in this exact file
//
// The FILE: form exists for the rare case where a single production file's
// entire purpose is to be a cross-package test-fixture/helper source (see
// the allowlist file's own header for the one entry that currently uses
// it), so the allowlist doesn't need one line per symbol to say the same
// thing 263 times.
func loadAllowlist(path string) (names map[string]bool, files map[string]bool) {
	names = map[string]bool{}
	files = map[string]bool{}
	data, err := os.ReadFile(path)
	if err != nil {
		return names, files
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		field := strings.Fields(line)[0]
		if rest, ok := strings.CutPrefix(field, "FILE:"); ok {
			files[rest] = true
			continue
		}
		names[field] = true
	}
	return names, files
}

// relFile returns abs relative to the current working directory, or abs
// unchanged if that fails.
func relFile(abs string) string {
	wd, err := os.Getwd()
	if err != nil {
		return abs
	}
	rel, err := filepath.Rel(wd, abs)
	if err != nil {
		return abs
	}
	return rel
}
