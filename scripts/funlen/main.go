// Command funlen lists every Go function or method whose total line span
// exceeds a threshold, for the function-size budget gate
// (scripts/check-function-budget.sh).
//
// Span = from the `func` keyword to the closing brace, inclusive; blank
// lines and comments count (docs/internal/architecture/draft-module-map.md,
// "Function length is the whole span"). Go has no notion of a "component",
// so the kind column is always "function" — it exists for a uniform
// four-column contract with scripts/tsfunlen.cjs, which does report
// "component".
//
// Output is split into a "## Production" and a "## Tests" section (by
// _test.go suffix) so the gate — and a human comparing this file's output to
// docs/internal/architecture/size-budget-grandfather-2026-09-15.md — can
// tell which functions are test code without a fifth column. Each data row
// is `lines<TAB>file:line<TAB>qualified name<TAB>kind`, longest first within
// its section. Lines starting with "#" are commentary, not data; the gate
// only trusts lines that start with a digit followed by a tab.
//
// Usage: go run ./scripts/funlen -root . [-limit 120]
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// funcRow is one scanned Go function or method.
type funcRow struct {
	file  string // path relative to -root
	name  string // qualified name, e.g. "restAPI.updateAgent"
	kind  string // always "function" for Go
	start int    // line of the `func` keyword
	lines int    // inclusive span, func keyword to closing brace
	test  bool   // true for a _test.go file
}

func main() {
	root := flag.String("root", ".", "repo root to scan")
	limit := flag.Int("limit", 120, "line threshold; only functions with more lines than this are reported")
	skip := flag.String("skip", "pkg/api/generated,pkg/gateway/spa,node_modules,.gitnexus,dist,vendor,.git",
		"comma-separated path fragments to skip entirely")
	flag.Parse()

	rows, totalProd, totalTest := scanRepo(*root, splitNonEmpty(*skip))
	printReport(filterOverLimit(rows, *limit), totalProd, totalTest, *limit)
}

func splitNonEmpty(csv string) []string {
	var out []string
	for _, s := range strings.Split(csv, ",") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// scanRepo walks root, parses every *.go file not under a skipped path, and
// returns every top-level function/method found plus the total count seen
// (before the over-limit filter), split production vs. test.
func scanRepo(root string, skips []string) (rows []funcRow, totalProd, totalTest int) {
	fset := token.NewFileSet()
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return ignoreWalkError(err)
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			rel = p
		}
		if shouldSkip(rel, skips) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		fileRows, isTest := scanFile(fset, p, rel)
		if isTest {
			totalTest += len(fileRows)
		} else {
			totalProd += len(fileRows)
		}
		rows = append(rows, fileRows...)
		return nil
	})
	if walkErr != nil {
		fmt.Fprintln(os.Stderr, "funlen: walk error:", walkErr)
	}
	return rows, totalProd, totalTest
}

// ignoreWalkError keeps scanRepo best-effort when WalkDir encounters an unreadable entry.
func ignoreWalkError(_ error) error {
	return nil
}

// shouldSkip reports whether rel (a root-relative path) falls under one of
// the skip fragments, matching a path component boundary rather than a bare
// substring so e.g. "dist" does not also skip "distillery/".
func shouldSkip(rel string, skips []string) bool {
	sep := string(filepath.Separator)
	for _, s := range skips {
		if rel == s || strings.HasPrefix(rel, s+sep) || strings.Contains(rel, sep+s+sep) {
			return true
		}
	}
	return false
}

// scanFile parses one Go file and returns every top-level FuncDecl with a
// body, plus whether the file is a _test.go file.
func scanFile(fset *token.FileSet, p, rel string) ([]funcRow, bool) {
	isTest := strings.HasSuffix(p, "_test.go")
	f, err := parser.ParseFile(fset, p, nil, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "funlen: parse error:", p, err)
		return nil, isTest
	}
	var rows []funcRow
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		start := fset.Position(fd.Pos()).Line
		end := fset.Position(fd.End()).Line
		rows = append(rows, funcRow{
			file:  rel,
			name:  qualifiedName(fd),
			kind:  "function",
			start: start,
			lines: end - start + 1,
			test:  isTest,
		})
	}
	return rows, isTest
}

// qualifiedName mirrors the naming in
// docs/internal/architecture/size-budget-grandfather-2026-09-15.md:
// "Receiver.Method" for methods, the bare name otherwise.
func qualifiedName(fd *ast.FuncDecl) string {
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		return recvName(fd.Recv.List[0].Type) + "." + fd.Name.Name
	}
	return fd.Name.Name
}

func recvName(t ast.Expr) string {
	switch x := t.(type) {
	case *ast.StarExpr:
		return recvName(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return recvName(x.X)
	case *ast.IndexListExpr:
		return recvName(x.X)
	}
	return "?"
}

func filterOverLimit(rows []funcRow, limit int) []funcRow {
	out := make([]funcRow, 0, len(rows))
	for _, r := range rows {
		if r.lines > limit {
			out = append(out, r)
		}
	}
	return out
}

func printReport(rows []funcRow, totalProd, totalTest, limit int) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].lines > rows[j].lines })
	var prodRows, testRows []funcRow
	for _, r := range rows {
		if r.test {
			testRows = append(testRows, r)
		} else {
			prodRows = append(prodRows, r)
		}
	}
	fmt.Printf("# Go functions longer than %d lines (span incl. signature and closing brace)\n", limit)
	fmt.Printf("# scanned: %d production funcs, %d test funcs\n", totalProd, totalTest)
	fmt.Printf("# over limit: %d production, %d test\n\n", len(prodRows), len(testRows))
	printSection("Production", prodRows)
	fmt.Println()
	printSection("Tests", testRows)
}

func printSection(title string, rows []funcRow) {
	fmt.Printf("## %s\n", title)
	fmt.Println("lines\tfile:line\tname\tkind")
	for _, r := range rows {
		fmt.Printf("%d\t%s:%d\t%s\t%s\n", r.lines, r.file, r.start, r.name, r.kind)
	}
}
