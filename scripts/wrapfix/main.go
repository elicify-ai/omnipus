// Command wrapfix mechanically rewrites wrapcheck findings in ./pkg and
// ./cmd: a flagged bare `return ..., err` (or a returned call whose error
// crosses a package or interface boundary) becomes
// `return ..., fmt.Errorf("<EnclosingFunc>: %w", err)`, adding the fmt
// import when missing. Sites it cannot rewrite safely are printed for
// manual handling — the helper never guesses.
//
// The message uses the enclosing function's name (receiver-qualified for
// methods, e.g. "Server.Start"): it is what a reader of the wrapped error
// needs in order to locate the failing operation.
//
// Safety rules, each producing a skip reason:
//   - the flagged expression must be a result of a return statement, at the
//     exact byte offset golangci-lint reported
//   - it must be the LAST result (Go convention: error is final)
//   - the innermost enclosing function must be a named FuncDecl, not a
//     closure — a closure has no name a reader could locate, so its wraps
//     are written by hand with whatever context is real
//   - the enclosing function's last result type must be exactly `error`
//   - no deferred func literal in the enclosing function may reassign the
//     error variable: the deferred write would wrap around (or clobber)
//     the rewrite
//   - only bare identifiers and call expressions are rewritten; anything
//     else (selectors, type assertions, ...) is left for manual review
//
// Idempotence: a rewritten return ends in a fmt.Errorf call, never a bare
// identifier, and wrapcheck does not flag wrapped returns — so a second
// run finds nothing to do.
//
// Usage:
//
//	go run ./scripts/wrapfix.go -dry-run     print the planned diff and skips
//	go run ./scripts/wrapfix.go              apply (re-runs golangci-lint)
//	go run ./scripts/wrapfix.go -findings f  reuse a saved findings JSON
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// lintIssue is one entry of golangci-lint's JSON report.
type lintIssue struct {
	FromLinter  string
	Text        string
	Pos         lintPos
	SourceLines []string
}

type lintPos struct {
	Filename string
	Offset   int
	Line     int
	Column   int
}

type lintReport struct {
	Issues []lintIssue
}

// edit is one byte-range replacement in the ORIGINAL file text.
type edit struct {
	start, end int
	text       string
	line       int // 1-based line of start, for dry-run reporting
}

// skip explains one finding the helper refuses to touch.
type skip struct {
	file, reason, src string
	line              int
}

// fileOutcome is the full plan for one file.
type fileOutcome struct {
	path     string
	rewrites int
	edits    []edit
	skips    []skip
	failed   string // non-empty: the file was left untouched, with why
}

func main() {
	findingsPath := flag.String("findings", "", "path to a golangci-lint JSON report (empty: run golangci-lint)")
	dryRun := flag.Bool("dry-run", false, "print the planned diff and skips without writing")
	flag.Parse()

	issues, err := loadFindings(*findingsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wrapfix:", err)
		os.Exit(1)
	}
	byFile := map[string][]lintIssue{}
	for _, is := range issues {
		if is.FromLinter != "wrapcheck" {
			continue
		}
		byFile[is.Pos.Filename] = append(byFile[is.Pos.Filename], is)
	}
	paths := make([]string, 0, len(byFile))
	for p := range byFile {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var found, rewritten, skipped, failed int
	reasons := map[string]int{}
	var allSkips []skip
	for _, p := range paths {
		out := planFile(p, byFile[p])
		found += len(byFile[p])
		rewritten += out.rewrites
		skipped += len(out.skips)
		for _, s := range out.skips {
			reasons[s.reason]++
		}
		allSkips = append(allSkips, out.skips...)
		if out.failed != "" {
			failed++
			fmt.Printf("FAILED %s: %s\n", p, out.failed)
			continue
		}
		if raw, err := os.ReadFile(p); err == nil {
			printDiff(out, raw)
		}
		if !*dryRun {
			if err := applyFile(out); err != nil {
				fmt.Printf("FAILED %s: %v\n", p, err)
				failed++
			}
		}
	}

	fmt.Printf("# wrapfix summary\n")
	fmt.Printf("# files with findings: %d\n", len(paths))
	fmt.Printf("# sites found:         %d\n", found)
	fmt.Printf("# sites rewritten:     %d\n", rewritten)
	fmt.Printf("# sites skipped:       %d\n", skipped)
	if failed > 0 {
		fmt.Printf("# files failed:        %d\n", failed)
	}
	if len(reasons) > 0 {
		fmt.Printf("# skip reasons:\n")
		for _, r := range sortedKeys(reasons) {
			fmt.Printf("#   %s: %d\n", r, reasons[r])
		}
		fmt.Printf("# skip list (for manual handling):\n")
		for _, s := range allSkips {
			fmt.Printf("#   %s:%d %s | %s\n", s.file, s.line, s.reason, s.src)
		}
	}
}

// loadFindings reads issues from a JSON file, or runs golangci-lint with a
// wrapcheck-only configuration and parses its JSON output. golangci-lint
// exits 1 when findings exist, which is success here; only a missing or
// unparseable report is an error.
func loadFindings(path string) ([]lintIssue, error) {
	if path == "" {
		p, err := runLint()
		if err != nil {
			return nil, err
		}
		path = p
		defer os.Remove(path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rep lintReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return rep.Issues, nil
}

// runLint invokes golangci-lint (wrapcheck only, repo build tags, no issue
// caps) writing JSON to a temp file, retrying while another golangci-lint
// instance holds the lock. It returns the temp file path.
func runLint() (string, error) {
	tmp, err := os.CreateTemp("", "wrapfix-findings-*.json")
	if err != nil {
		return "", err
	}
	tmp.Close()
	args := []string{
		"run",
		"--enable-only=wrapcheck",
		"--build-tags=goolm,stdjson",
		"--timeout=20m",
		"--max-issues-per-linter=0",
		"--max-same-issues=0",
		"--output.json.path=" + tmp.Name(),
		"./pkg/...", "./cmd/...",
	}
	for attempt := 1; ; attempt++ {
		cmd := exec.Command("golangci-lint", args...)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		if runErr == nil {
			return tmp.Name(), nil
		}
		msg := stderr.String()
		if attempt < 6 && strings.Contains(msg, "parallel golangci-lint") {
			fmt.Fprintf(os.Stderr, "wrapfix: golangci-lint lock busy, retry %d/5\n", attempt)
			time.Sleep(15 * time.Second)
			continue
		}
		// Exit code 1 with a usable report just means findings exist.
		if info, statErr := os.Stat(tmp.Name()); statErr == nil && info.Size() > 2 {
			return tmp.Name(), nil
		}
		return "", fmt.Errorf("golangci-lint: %v: %s", runErr, msg)
	}
}

// printDiff shows each edit as a -/+ line pair for the line it rewrites,
// plus a per-file headline. In write mode it doubles as the applied-change
// log. Only single-line rewrites print a full line pair; the fmt-import
// insertions (end == start, whole-line text) print their inserted text.
func printDiff(out fileOutcome, src []byte) {
	if out.rewrites == 0 && len(out.skips) == 0 {
		return
	}
	fmt.Printf("── %s: %d rewrite(s), %d skip(s)\n", out.path, out.rewrites, len(out.skips))
	for _, e := range out.edits {
		ls, le := lineStart(src, e.start), lineEnd(src, e.end)
		old := strings.TrimRight(string(src[ls:le]), "\r\n")
		newLine := string(src[ls:e.start]) + e.text + string(src[e.end:le])
		newLine = strings.TrimRight(newLine, "\r\n")
		fmt.Printf("%s:%d\n-%s\n+%s\n", out.path, e.line, old, newLine)
	}
}

// planFile reads and parses one file, classifies each of its findings, and
// returns the edits (rewrites + fmt import) and skips. It never writes.
func planFile(path string, issues []lintIssue) fileOutcome {
	out := fileOutcome{path: path}
	src, err := os.ReadFile(path)
	if err != nil {
		out.failed = err.Error()
		return out
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		out.failed = err.Error()
		return out
	}
	funcs := collectFuncRanges(fset, file)
	var sites []retSite
	ast.Inspect(file, func(n ast.Node) bool {
		if r, ok := n.(*ast.ReturnStmt); ok {
			sites = append(sites, retSite{
				ret:   r,
				start: fset.Position(r.Pos()).Offset,
				end:   fset.Position(r.End()).Offset,
			})
		}
		return true
	})

	for _, is := range issues {
		expr, site, status := matchReturn(fset, sites, is.Pos.Offset)
		reason := classifySkip(expr, site, status, funcs)
		if reason != "" {
			out.skips = append(out.skips, skip{
				file:   path,
				line:   is.Pos.Line,
				reason: reason,
				src:    firstSource(is),
			})
			continue
		}
		start, end := fset.Position(expr.Pos()).Offset, fset.Position(expr.End()).Offset
		out.edits = append(out.edits, edit{
			start: start,
			end:   end,
			text:  fmt.Sprintf("fmt.Errorf(%q, %s)", funcLabel(enclosingDecl(site, funcs))+": %w", src[start:end]),
			line:  fset.Position(expr.Pos()).Line,
		})
		out.rewrites++
	}
	if out.rewrites > 0 && !importsFmt(file) {
		if imp, ok := fmtImportEdit(fset, file, src); ok {
			out.edits = append(out.edits, imp)
		} else {
			out.failed = "cannot place the fmt import"
			out.rewrites = 0
			out.edits = nil
		}
	}
	return out
}

// firstSource returns the first source line golangci quoted, for skip reports.
func firstSource(is lintIssue) string {
	if len(is.SourceLines) == 0 {
		return ""
	}
	return strings.TrimSpace(is.SourceLines[0])
}

// retSite is a return statement with its byte-offset span, so every
// comparison against golangci-lint's Pos.Offset stays in one unit system.
type retSite struct {
	ret        *ast.ReturnStmt
	start, end int // byte offsets into the file
}

// matchReturn finds the return statement containing byte offset off and the
// result expression starting exactly at off. A return whose result
// expression is a func literal contains that literal's own returns in its
// span, so containment alone is ambiguous — the TIGHTEST containing return
// is the flagged one.
func matchReturn(fset *token.FileSet, sites []retSite, off int) (ast.Expr, retSite, string) {
	var best retSite
	found := false
	for _, s := range sites {
		if off < s.start || off > s.end {
			continue
		}
		if !found || (s.end-s.start) < (best.end-best.start) {
			best, found = s, true
		}
	}
	if !found {
		return nil, retSite{}, "notfound"
	}
	for _, res := range best.ret.Results {
		if fset.Position(res.Pos()).Offset == off {
			return res, best, "matched"
		}
	}
	if off == best.start {
		return nil, best, "naked"
	}
	return nil, best, "inside"
}

// classifySkip returns "" when the site is safe to rewrite, else the reason.
func classifySkip(expr ast.Expr, site retSite, status string, funcs []funcRange) string {
	switch status {
	case "notfound":
		return "flagged offset is not a return statement result"
	case "naked":
		return "naked return (named result) — restructure by hand"
	case "inside":
		return "flagged offset is inside a return but not at a result expression"
	}
	ret := site.ret
	if len(ret.Results) == 0 || ret.Results[len(ret.Results)-1] != expr {
		return "flagged expression is not the last result"
	}
	enclosing := enclosingDecl(site, funcs)
	innermost := tightestFunc(site, funcs)
	if innermost != nil && innermost.lit {
		return "inside a closure — no reliable function name"
	}
	if enclosing == nil {
		return "no enclosing named function (top-level closure)"
	}
	if !lastResultIsError(enclosing) {
		return "enclosing function's last result is not error"
	}
	// A return with k expressions in an n-result function expands the LAST
	// expression to n-(k-1) values (`return twoValueCall()` fills both
	// slots). Only a single-value expansion can be wrapped inline; anything
	// else needs a temp variable and is restructured by hand.
	if n, k := resultArity(enclosing), len(ret.Results); n-(k-1) != 1 {
		return "return's last expression expands to multiple results — restructure by hand"
	}
	switch e := expr.(type) {
	case *ast.Ident:
		if e.Name == "_" {
			return "blank identifier"
		}
		if deferReassigns(enclosing, e.Name) {
			return "deferred func literal reassigns the error variable"
		}
		if !guardedByNilCheck(site, e.Name) {
			return "tail return of a possibly-nil error — wrap by hand under a nil check"
		}
	case *ast.CallExpr:
		// A call result is never wrapped inline: the call may return nil on
		// success, and fmt.Errorf("x: %w", nil) manufactures a failure out
		// of a success (observed live: flockExclusive turned every
		// successful flock into an error). Call returns are restructured by
		// hand into `v, err := call; if err != nil { ... }`.
		return "returned call expression — restructure by hand under a nil check"
	default:
		return fmt.Sprintf("unsupported expression shape %T", expr)
	}
	return ""
}

// guardedByNilCheck reports whether the return sits inside a block whose
// condition proves the named error non-nil (`if err != nil { ... return err }`).
// A tail `return err` outside such a block carries nil on success and must
// not be wrapped.
func guardedByNilCheck(src []byte, line int, name string) bool {
	lines := strings.Split(string(src), "\n")
	ln := line - 1
	if ln < 0 || ln >= len(lines) {
		return false
	}
	mine := len(lines[ln]) - len(strings.TrimLeft(lines[ln], "\t"))
	for k := ln - 1; k >= 0; k-- {
		t := strings.TrimRight(lines[k], " \t")
		ind := len(lines[k]) - len(strings.TrimLeft(lines[k], "\t"))
		if ind >= mine {
			continue
		}
		if !strings.HasSuffix(t, "{") {
			continue
		}
		cond := strings.TrimSpace(t)
		if strings.Contains(cond, name+" != nil") {
			return true
		}
		if strings.HasPrefix(cond, "func ") || strings.HasPrefix(cond, "for ") || strings.HasPrefix(cond, "switch ") {
			return false
		}
	}
	return false
}

// funcRange is the byte range of one FuncDecl or FuncLit.
type funcRange struct {
	start, end int
	decl       *ast.FuncDecl // nil for a literal
	lit        bool
}

// collectFuncRanges returns the ranges of every FuncDecl and FuncLit.
func collectFuncRanges(fset *token.FileSet, file *ast.File) []funcRange {
	var funcs []funcRange
	add := func(n ast.Node, decl *ast.FuncDecl, lit bool) {
		funcs = append(funcs, funcRange{
			start: fset.Position(n.Pos()).Offset,
			end:   fset.Position(n.End()).Offset,
			decl:  decl,
			lit:   lit,
		})
	}
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
			add(fd, fd, false)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if fl, ok := n.(*ast.FuncLit); ok {
			add(fl, nil, true)
		}
		return true
	})
	return funcs
}

// contains reports whether r spans the whole return statement.
func (r funcRange) contains(site retSite) bool {
	return r.start <= site.start && site.end <= r.end
}

// tightestFunc returns the smallest-span function range enclosing the site.
func tightestFunc(site retSite, funcs []funcRange) *funcRange {
	var best *funcRange
	for i := range funcs {
		if funcs[i].contains(site) && (best == nil || span(funcs[i]) < span(*best)) {
			best = &funcs[i]
		}
	}
	return best
}

// enclosingDecl returns the innermost FuncDecl whose range contains the site.
func enclosingDecl(site retSite, funcs []funcRange) *ast.FuncDecl {
	var best *ast.FuncDecl
	bestSpan := 1 << 60
	for i := range funcs {
		f := funcs[i]
		if f.decl != nil && f.contains(site) && (f.end-f.start) < bestSpan {
			best = f.decl
			bestSpan = f.end - f.start
		}
	}
	return best
}

func span(r funcRange) int { return r.end - r.start }

// lastResultIsError reports whether fd's final result type is exactly the
// predeclared `error` — anything else (custom error types, generics) needs
// a human, because wrapping would change the returned type.
func lastResultIsError(fd *ast.FuncDecl) bool {
	if fd.Type == nil || fd.Type.Results == nil || len(fd.Type.Results.List) == 0 {
		return false
	}
	last := fd.Type.Results.List[len(fd.Type.Results.List)-1].Type
	id, ok := last.(*ast.Ident)
	return ok && id.Name == "error"
}

// resultArity counts fd's result slots, unpacking grouped names: `(a, b
// error)` is one ast field carrying two results.
func resultArity(fd *ast.FuncDecl) int {
	if fd.Type == nil || fd.Type.Results == nil {
		return 0
	}
	n := 0
	for _, f := range fd.Type.Results.List {
		if len(f.Names) == 0 {
			n++
		} else {
			n += len(f.Names)
		}
	}
	return n
}

// deferReassigns reports whether any deferred func literal inside fd
// assigns to a variable named name, which would interact with the rewrite.
func deferReassigns(fd *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		d, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		lit, ok := d.Call.Fun.(*ast.FuncLit)
		if !ok {
			return true
		}
		ast.Inspect(lit.Body, func(m ast.Node) bool {
			as, ok := m.(*ast.AssignStmt)
			if !ok || as.Tok != token.ASSIGN {
				return true
			}
			for _, l := range as.Lhs {
				if id, ok := l.(*ast.Ident); ok && id.Name == name {
					found = true
				}
			}
			return true
		})
		return true
	})
	return found
}

// isFmtErrorf reports whether call is fmt.Errorf(...).
func isFmtErrorf(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "fmt" && sel.Sel.Name == "Errorf"
}

// funcLabel renders fd's name for the wrap message: "Recv.Method" for
// methods (pointer and generics stripped), the bare name otherwise.
func funcLabel(fd *ast.FuncDecl) string {
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		return recvTypeName(fd.Recv.List[0].Type) + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// recvTypeName reduces a receiver type expression to its type name.
func recvTypeName(t ast.Expr) string {
	switch x := t.(type) {
	case *ast.StarExpr:
		return recvTypeName(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return recvTypeName(x.X)
	case *ast.IndexListExpr:
		return recvTypeName(x.X)
	}
	return "?"
}

// importsFmt reports whether the file already imports "fmt".
func importsFmt(file *ast.File) bool {
	for _, imp := range file.Imports {
		if path, err := strconv.Unquote(imp.Path.Value); err == nil && path == "fmt" {
			return true
		}
	}
	return false
}

// fmtImportEdit builds an edit inserting `"fmt"` at its sorted position in
// the first import group, or after the package clause when the file has no
// imports. The result is gofmt-stable: format.Source normalizes spacing.
func fmtImportEdit(fset *token.FileSet, file *ast.File, src []byte) (edit, bool) {
	var gen *ast.GenDecl
	for _, d := range file.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			gen = gd
			break
		}
	}
	if gen == nil {
		off := fset.Position(file.Name.End()).Offset
		return edit{start: off, end: off, text: "\n\nimport \"fmt\"", line: fset.Position(file.Name.Pos()).Line}, true
	}
	if !gen.Lparen.IsValid() {
		// Single-spec declaration: add a sibling decl on the sorted side.
		spec := gen.Specs[0]
		path, _ := strconv.Unquote(spec.(*ast.ImportSpec).Path.Value)
		if "fmt" < path {
			off := lineStart(src, fset.Position(gen.Pos()).Offset)
			return edit{start: off, end: off, text: "import \"fmt\"\n\n", line: fset.Position(gen.Pos()).Line}, true
		}
		off := lineEnd(src, fset.Position(gen.End()).Offset)
		return edit{start: off, end: off, text: "\n\nimport \"fmt\"", line: fset.Position(gen.End()).Line}, true
	}
	// Parenthesized block: insert into the FIRST group (contiguous specs)
	// at the sorted slot.
	specs := gen.Specs
	groupEnd := len(specs)
	for i := 1; i < len(specs); i++ {
		if fset.Position(specs[i-1].End()).Line < fset.Position(specs[i].Pos()).Line-1 {
			groupEnd = i
			break
		}
	}
	for i := 0; i < groupEnd; i++ {
		path, _ := strconv.Unquote(specs[i].(*ast.ImportSpec).Path.Value)
		if "fmt" < path {
			off := lineStart(src, fset.Position(specs[i].Pos()).Offset)
			return edit{start: off, end: off, text: "\t\"fmt\"\n", line: fset.Position(specs[i].Pos()).Line}, true
		}
	}
	// Append at the end of the first group: before the next group's first
	// spec, or before the closing paren line.
	var anchor ast.Node = specs[groupEnd-1]
	anchorLineOff := lineEnd(src, fset.Position(anchor.End()).Offset)
	text := "\n\t\"fmt\""
	if groupEnd < len(specs) {
		anchor = specs[groupEnd]
		anchorLineOff = lineStart(src, fset.Position(anchor.Pos()).Offset)
		text = "\t\"fmt\"\n"
	}
	return edit{start: anchorLineOff, end: anchorLineOff, text: text, line: fset.Position(anchor.Pos()).Line}, true
}

// lineStart returns the offset of the first byte of the line containing off.
func lineStart(src []byte, off int) int {
	for off > 0 && src[off-1] != '\n' {
		off--
	}
	return off
}

// lineEnd returns the offset just past the newline of the line containing
// off (or len(src) for the final line).
func lineEnd(src []byte, off int) int {
	for off < len(src) && src[off] != '\n' {
		off++
	}
	if off < len(src) {
		off++ // include the newline
	}
	return off
}

// applyFile applies the planned edits, formats the result, and writes the
// file back with its original permissions.
func applyFile(out fileOutcome) error {
	if len(out.edits) == 0 {
		return nil
	}
	raw, err := os.ReadFile(out.path)
	if err != nil {
		return err
	}
	info, err := os.Stat(out.path)
	if err != nil {
		return err
	}
	edits := append([]edit(nil), out.edits...)
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	for i := 1; i < len(edits); i++ {
		if edits[i].start < edits[i-1].end {
			return fmt.Errorf("overlapping edits at offset %d", edits[i].start)
		}
	}
	var b []byte
	prev := 0
	for _, e := range edits {
		b = append(b, raw[prev:e.start]...)
		b = append(b, e.text...)
		prev = e.end
	}
	b = append(b, raw[prev:]...)
	formatted, err := format.Source(b)
	if err != nil {
		return fmt.Errorf("rewritten file does not format: %w", err)
	}
	return os.WriteFile(out.path, formatted, info.Mode().Perm())
}

// sortedKeys returns map keys in sorted order (string values).
func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
