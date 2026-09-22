// Command unsafe-error-wrap flags the two wrapcheck-adjacent defect classes
// that broke 18 packages when wrapcheck was first enabled (reverted at
// ed86a62f9):
//
//  1. Wrapping a sentinel whose identity is the contract. io.Reader.Read
//     must return io.EOF bare — the standard library compares err == io.EOF,
//     which errors.Is does not rescue. The same applies to Write/Seek/ReadAt/
//     WriteAt. Wrapping the io.EOF selector itself is flagged anywhere.
//
//  2. Wrapping a call that may return nil: fmt.Errorf("...: %w", f()) turns
//     a nil success into a non-nil error whose message is `...: %!w(<nil>)`.
//     The shipped boundedBody.Read defect was this shape on an io.Reader
//     method (caught by rule 1). Same-package helpers and constructors
//     (fmt.Errorf, errors.New, New*, *.Err()) are allowed.
//
// Usage: go run ./scripts/unsafe-error-wrap -root <dir>
// Exit: 0 clean, 1 findings, 2 the scanner itself could not run.
package main

import (
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type finding struct {
	file, reason, snippet string
	line                  int
}

func main() {
	root := flag.String("root", ".", "repo root to scan")
	paths := flag.String("paths", "pkg,cmd", "comma-separated dirs relative to -root")
	skip := flag.String("skip", "pkg/api/generated,pkg/gateway/spa,node_modules,.gitnexus,dist,vendor,.git",
		"comma-separated path fragments to skip entirely")
	flag.Parse()

	findings, err := scanRepo(*root, splitCSV(*paths), splitCSV(*skip))
	if err != nil {
		fmt.Fprintf(os.Stderr, "unsafe-error-wrap: %v\n", err)
		os.Exit(2)
	}
	if len(findings) == 0 {
		fmt.Println("OK: no unsafe error wraps")
		return
	}
	fmt.Printf("check-no-unsafe-error-wrap: %d unsafe wrap(s):\n\n", len(findings))
	for _, f := range findings {
		fmt.Printf("  %s:%d: %s\n", f.file, f.line, f.reason)
		if f.snippet != "" {
			fmt.Printf("    %s\n", f.snippet)
		}
	}
	os.Exit(1)
}

func splitCSV(csv string) []string {
	var out []string
	for _, s := range strings.Split(csv, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func scanRepo(root string, paths, skips []string) ([]finding, error) {
	var all []finding
	for _, rel := range paths {
		dir := filepath.Join(root, rel)
		info, err := os.Stat(dir)
		if err != nil {
			return nil, fmt.Errorf("scan path %s: %w", rel, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("scan path %s is not a directory", rel)
		}
		found, err := scanDir(root, dir, skips)
		if err != nil {
			return nil, err
		}
		all = append(all, found...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].file != all[j].file {
			return all[i].file < all[j].file
		}
		return all[i].line < all[j].line
	})
	return all, nil
}

func scanDir(root, dir string, skips []string) ([]finding, error) {
	var all []finding
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			rel = p
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name(), rel, skips) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		found, scanErr := scanFile(root, p)
		if scanErr != nil {
			return fmt.Errorf("%s: %w", rel, scanErr)
		}
		all = append(all, found...)
		return nil
	})
	return all, err
}

func shouldSkipDir(name, rel string, skips []string) bool {
	if name == "testdata" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") {
		return true
	}
	rel = filepath.ToSlash(rel)
	for _, s := range skips {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	return false
}

func scanFile(root, path string) ([]finding, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	rel, relErr := filepath.Rel(root, path)
	if relErr != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	var findings []finding
	findings = append(findings, inspectLegacyOSIs(file, fset, src, rel)...)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		findings = append(findings, inspectFunc(fn, fset, src, rel)...)
	}
	return findings, nil
}

func inspectLegacyOSIs(file *ast.File, fset *token.FileSet, src []byte, rel string) []finding {
	var findings []finding
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "os" {
			return true
		}
		var sentinel string
		switch sel.Sel.Name {
		case "IsNotExist":
			sentinel = "os.ErrNotExist"
		case "IsExist":
			sentinel = "os.ErrExist"
		case "IsPermission":
			sentinel = "os.ErrPermission"
		default:
			return true
		}
		pos := fset.Position(call.Pos())
		findings = append(findings, finding{
			file:    rel,
			line:    pos.Line,
			reason:  "legacy " + pkg.Name + "." + sel.Sel.Name + " does not unwrap; use errors.Is(err, " + sentinel + ")",
			snippet: snippetAt(src, pos.Offset),
		})
		return true
	})
	return findings
}

func inspectFunc(fn *ast.FuncDecl, fset *token.FileSet, src []byte, rel string) []finding {
	ioMethod := isIOStreamMethod(fn)
	var findings []finding
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isFmtErrorf(call) {
			return true
		}
		format, ok := stringLit(call.Args, 0)
		if !ok || !strings.Contains(format, "%w") {
			return true
		}
		for _, idx := range wrapArgIndexes(format) {
			arg, ok := variadicArg(call, idx)
			if !ok {
				continue
			}
			reason := classifyWrap(arg, fn, ioMethod)
			if reason == "" {
				continue
			}
			pos := fset.Position(call.Pos())
			findings = append(findings, finding{
				file:    rel,
				line:    pos.Line,
				reason:  reason,
				snippet: snippetAt(src, pos.Offset),
			})
		}
		return true
	})
	return findings
}

func classifyWrap(arg ast.Expr, fn *ast.FuncDecl, ioMethod bool) string {
	if ioMethod {
		return "wrap in " + fn.Name.Name + " (io.EOF must be returned bare; the standard library compares err == io.EOF)"
	}
	switch a := arg.(type) {
	case *ast.CallExpr:
		if isNeverNilCall(a) {
			return ""
		}
		return "wrap of a call that may return nil (fmt.Errorf(\"...: %w\", f()) turns success into %!w(<nil>))"
	case *ast.Ident:
		if a.Name == "nil" {
			return "wrap of nil"
		}
		return ""
	case *ast.SelectorExpr:
		if name, ok := sentinelName(a); ok && isStreamSentinel(name) {
			return "wrap of sentinel " + name + " whose identity is the contract (callers compare with ==)"
		}
		return ""
	default:
		return ""
	}
}

func isFmtErrorf(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "fmt" && sel.Sel.Name == "Errorf"
}

func isNeverNilCall(call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		// Same-package helpers (cacheAgeAnnotate, wrapFSErr) are not the
		// `fmt.Errorf("%w", os.Open(...))` defect.
		return true
	case *ast.SelectorExpr:
		name := fun.Sel.Name
		if name == "Err" || name == "err" || name == "Cause" || name == "Join" ||
			name == "New" || name == "Errorf" || strings.HasPrefix(name, "New") ||
			(strings.HasSuffix(name, "Error") && name != "Error") {
			return true
		}
		pkg, ok := fun.X.(*ast.Ident)
		if !ok {
			return false
		}
		switch pkg.Name + "." + name {
		case "fmt.Errorf", "errors.New", "errors.Errorf", "errors.Join", "context.Cause":
			return true
		}
		return isPackageSentinelIdent(name)
	}
	return false
}

func isPackageSentinelIdent(name string) bool {
	if name == "errno" || strings.HasSuffix(name, "Errno") {
		return true
	}
	if strings.HasPrefix(name, "Err") && len(name) > 3 {
		return true
	}
	if strings.HasPrefix(name, "err") && len(name) > 3 {
		r := name[3]
		return r >= 'A' && r <= 'Z'
	}
	return false
}

func sentinelName(e ast.Expr) (string, bool) {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	return pkg.Name + "." + sel.Sel.Name, true
}

func isStreamSentinel(name string) bool {
	switch name {
	case "io.EOF", "io.ErrUnexpectedEOF", "io.ErrClosedPipe", "io.ErrNoProgress",
		"io.ErrShortBuffer", "io.ErrShortWrite":
		return true
	}
	return false
}

func isIOStreamMethod(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || fn.Name == nil {
		return false
	}
	if !lastResultIsError(fn) {
		return false
	}
	switch fn.Name.Name {
	case "Read", "Write", "ReadAt", "WriteAt":
		return firstParamIsByteSlice(fn)
	case "Seek":
		return true
	default:
		return false
	}
}

func lastResultIsError(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
		return false
	}
	last := fn.Type.Results.List[len(fn.Type.Results.List)-1]
	id, ok := last.Type.(*ast.Ident)
	return ok && id.Name == "error"
}

func firstParamIsByteSlice(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return false
	}
	arr, ok := fn.Type.Params.List[0].Type.(*ast.ArrayType)
	if !ok || arr.Len != nil {
		return false
	}
	id, ok := arr.Elt.(*ast.Ident)
	return ok && id.Name == "byte"
}

func stringLit(args []ast.Expr, i int) (string, bool) {
	if i >= len(args) {
		return "", false
	}
	lit, ok := args[i].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func variadicArg(call *ast.CallExpr, oneBased int) (ast.Expr, bool) {
	if oneBased <= 0 || oneBased >= len(call.Args) {
		return nil, false
	}
	return call.Args[oneBased], true
}

func wrapArgIndexes(format string) []int {
	var idxs []int
	next := 1
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		if i+1 < len(format) && format[i+1] == '%' {
			i++
			continue
		}
		i++
		explicit := 0
		if i < len(format) && format[i] == '[' {
			end := strings.IndexByte(format[i:], ']')
			if end > 1 {
				n, err := strconv.Atoi(format[i+1 : i+end])
				if err == nil {
					explicit = n
				}
				i += end + 1
			}
		}
		for i < len(format) && strings.ContainsRune("#+- 0", rune(format[i])) {
			i++
		}
		for i < len(format) && format[i] >= '0' && format[i] <= '9' {
			i++
		}
		if i < len(format) && format[i] == '.' {
			i++
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				i++
			}
		}
		if i >= len(format) {
			break
		}
		if format[i] == 'w' {
			if explicit != 0 {
				idxs = append(idxs, explicit)
			} else {
				idxs = append(idxs, next)
			}
		}
		if explicit != 0 {
			next = explicit + 1
		} else {
			next++
		}
	}
	return idxs
}

func snippetAt(src []byte, off int) string {
	if off < 0 || off > len(src) {
		return ""
	}
	start := off
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	end := off
	for end < len(src) && src[end] != '\n' {
		end++
	}
	return strings.TrimSpace(string(src[start:end]))
}
