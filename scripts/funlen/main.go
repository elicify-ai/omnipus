// Command funlen lists every Go function or method whose code-line count
// exceeds a threshold, for the function-size budget gate
// (scripts/check-function-budget.sh).
//
// The span is still from the `func` keyword to the closing brace. Only code
// lines inside that span count: a line counts when a non-comment token covers
// a non-whitespace character on it. Blank lines and comment-only lines do not
// (founder decision 2026-11-07, option C; docs/internal/architecture/draft-module-map.md,
// "Function length is code lines only").
//
// A function may carry a directive in the // comment block immediately above
// it (no blank line between that block and the func line):
//
//	//budget:exception <one-line reason>
//	// budget:exception <one-line reason>
//
// Both spellings are accepted. An empty reason is reported as an empty
// exception (the gate fails it). The directive is not inside the span, so it
// does not itself count as a line.
//
// Go has no notion of a "component", so the kind column is always "function".
// It exists for a uniform column contract with scripts/tsfunlen.cjs.
//
// Output is split into a "## Production" and a "## Tests" section (by
// _test.go suffix). Each data row is
// `lines<TAB>file:line<TAB>qualified name<TAB>kind<TAB>exception`,
// longest first within its section. The exception column is `0` (none),
// `1` (directive present, reason empty), or `2` plus the reason.
// Lines starting with "#" are commentary, not data; the gate only trusts
// lines that start with a digit followed by a tab.
//
// Usage: go run ./scripts/funlen -root . [-limit 120]
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// budgetDirective matches a one-line exception marker. The reason, if any, is group 1.
var budgetDirective = regexp.MustCompile(`^//\s*budget:exception(?:\s(.*))?$`)

// funcRow is one scanned Go function or method.
type funcRow struct {
	file      string // path relative to -root
	name      string // qualified name, e.g. "restAPI.updateAgent"
	kind      string // always "function" for Go
	start     int    // line of the `func` keyword (parser line, same as before)
	lines     int    // code lines inside the span, not blanks or comment-only lines
	test      bool   // true for a _test.go file
	exception string // "" none, "!" empty reason, otherwise the reason
}

func main() {
	root := flag.String("root", ".", "repo root to scan")
	limit := flag.Int("limit", 120, "code-line threshold; functions over this, and any with an exception directive, are reported")
	skip := flag.String("skip", "pkg/api/generated,pkg/gateway/spa,node_modules,.gitnexus,dist,vendor,.git",
		"comma-separated path fragments to skip entirely")
	flag.Parse()

	rows, totalProd, totalTest := scanRepo(*root, splitNonEmpty(*skip))
	printReport(filterReported(rows, *limit), totalProd, totalTest, *limit)
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
			// A path that vanished mid-walk, or one we may not read, is not this
			// scanner's business. ANY other error is a real I/O failure and must
			// stop the scan: silently skipping it would under-report function
			// sizes and let the budget gates pass on an incomplete measurement.
			if errorsIsSkippable(err) {
				return nil
			}
			return err
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

func errorsIsSkippable(err error) bool {
	return os.IsNotExist(err) || os.IsPermission(err)
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
	src, err := os.ReadFile(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "funlen: read error:", p, err)
		return nil, isTest
	}
	f, err := parser.ParseFile(fset, p, src, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "funlen: parse error:", p, err)
		return nil, isTest
	}
	starts := lineStarts(src)
	codeLines := codeLinesIn(src, starts)
	srcLines := strings.Split(string(src), "\n")
	var rows []funcRow
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		rows = append(rows, rowForFunc(fset, src, starts, codeLines, srcLines, rel, isTest, fd))
	}
	return rows, isTest
}

func rowForFunc(fset *token.FileSet, src []byte, starts []int, codeLines map[int]struct{}, srcLines []string, rel string, isTest bool, fd *ast.FuncDecl) funcRow {
	file := fset.File(fd.Pos())
	startOff := file.Offset(fd.Pos())
	endOff := file.Offset(fd.End())
	physStart := lineAt(starts, startOff)
	physEnd := physStart
	if endOff > startOff {
		last := endOff - 1
		if last >= len(src) {
			last = len(src) - 1
		}
		if last < 0 {
			last = 0
		}
		physEnd = lineAt(starts, last)
	}
	return funcRow{
		file:      rel,
		name:      qualifiedName(fd),
		kind:      "function",
		start:     fset.Position(fd.Pos()).Line,
		lines:     countCode(codeLines, physStart, physEnd),
		test:      isTest,
		exception: exceptionAbove(srcLines, physStart),
	}
}

// lineStarts holds the byte offset of the first byte of each physical line.
func lineStarts(src []byte) []int {
	starts := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' && i+1 < len(src) {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// lineAt returns the 1-based physical line containing byte offset off.
func lineAt(starts []int, off int) int {
	if off < 0 {
		off = 0
	}
	i := sort.Search(len(starts), func(i int) bool { return starts[i] > off }) - 1
	if i < 0 {
		return 1
	}
	return i + 1
}

// codeLinesIn records physical lines that contain a non-comment, non-whitespace
// token character. Comment tokens and inserted semicolons do not mark a line.
func codeLinesIn(src []byte, starts []int) map[int]struct{} {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, scanner.ScanComments)
	lines := map[int]struct{}{}
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		end := s.End()
		if tok == token.COMMENT || (tok == token.SEMICOLON && lit == "\n") {
			continue
		}
		markToken(src, file, starts, lines, pos, end)
	}
	return lines
}

func markToken(src []byte, file *token.File, starts []int, lines map[int]struct{}, pos, end token.Pos) {
	if !pos.IsValid() || !end.IsValid() {
		return
	}
	startOff := file.Offset(pos)
	endOff := file.Offset(end)
	if startOff < 0 {
		startOff = 0
	}
	if endOff > len(src) {
		endOff = len(src)
	}
	if endOff <= startOff {
		return
	}
	i := startOff
	for i < endOff {
		lineEnd := bytes.IndexByte(src[i:endOff], '\n')
		segEnd := endOff
		if lineEnd >= 0 {
			segEnd = i + lineEnd
		}
		if hasNonSpace(src[i:segEnd]) {
			lines[lineAt(starts, i)] = struct{}{}
		}
		if lineEnd < 0 {
			break
		}
		i = segEnd + 1
	}
}

func hasNonSpace(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\r', '\v', '\f':
			continue
		default:
			return true
		}
	}
	return false
}

func countCode(code map[int]struct{}, first, last int) int {
	n := 0
	for l := first; l <= last; l++ {
		if _, ok := code[l]; ok {
			n++
		}
	}
	return n
}

// exceptionAbove returns the closest budget directive in the // comment block
// that ends on the line immediately above startLine (1-based). A blank line
// or a non-comment line ends the block. "" means no directive, "!" means the
// directive is present with an empty reason.
func exceptionAbove(srcLines []string, startLine int) string {
	for i := startLine - 2; i >= 0 && i < len(srcLines); i-- {
		trim := strings.TrimSpace(strings.TrimRight(srcLines[i], "\r"))
		if trim == "" || !strings.HasPrefix(trim, "//") {
			break
		}
		if reason, ok := directiveReason(trim); ok {
			return reason
		}
	}
	return ""
}

func directiveReason(line string) (string, bool) {
	m := budgetDirective.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	reason := ""
	if len(m) > 1 {
		reason = strings.TrimSpace(m[1])
	}
	if reason == "" {
		return "!", true
	}
	return reason, true
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

func filterReported(rows []funcRow, limit int) []funcRow {
	out := make([]funcRow, 0, len(rows))
	for _, r := range rows {
		if r.lines > limit || r.exception != "" {
			out = append(out, r)
		}
	}
	return out
}

func formatException(exc string) string {
	switch exc {
	case "":
		return "0"
	case "!":
		return "1"
	default:
		return "2" + sanitizeReason(exc)
	}
}

func sanitizeReason(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
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
	fmt.Printf("# Go functions over %d code lines (blank and comment-only lines excluded)\n", limit)
	fmt.Printf("# scanned: %d production funcs, %d test funcs\n", totalProd, totalTest)
	fmt.Printf("# reported: %d production, %d test (over the limit, or carrying an exception directive)\n\n", len(prodRows), len(testRows))
	printSection("Production", prodRows)
	fmt.Println()
	printSection("Tests", testRows)
}

func printSection(title string, rows []funcRow) {
	fmt.Printf("## %s\n", title)
	fmt.Println("lines\tfile:line\tname\tkind\texception")
	for _, r := range rows {
		fmt.Printf("%d\t%s:%d\t%s\t%s\t%s\n", r.lines, r.file, r.start, r.name, r.kind, formatException(r.exception))
	}
}
