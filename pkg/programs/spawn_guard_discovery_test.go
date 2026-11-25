// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: S-106 (spawn guard) and CHECK R06 — the per-site inventory and
// the helper-aware discovery the first spawn guard left as loud blockers.
//
// This file extends the same intraprocedural AST instrument as
// spawn_guard_test.go; it adds discovery and never loosens a rule:
//
//   - a launch of a *exec.Cmd PARAMETER is a site (a helper that receives a
//     command and starts it), judged per command like every other launch;
//   - a call that hands a locally constructed command to a launcher helper
//     (a plain call, or a method call on a value, for every function the
//     table lists with kind "launcher") is that command's launch, so the
//     caller's same-command Attach is what the guard checks;
//   - the table lists how many launches each function holds, so a launch
//     added to (or dropped from) a listed function is classified by its own
//     row, and a row that no discovered launch matches is flagged as vacuous.
//
// Residual limits, not closed here: the analysis stays inside one function
// body. A command constructed in one function and started through a struct
// field, a returned value or a closure parameter, a launcher in another
// package, a bare in-package call of a sink (inside pkg/sandbox itself) and a
// child started by a runner that builds its own command from an argv (for
// example sandbox.Run, which the git version probe uses) are not discovered.
// The tree-wide detector in spawn_guard_tree_test.go bounds that risk for
// program variables; the runtime campaigns in pkg/gateway observe the sinks.

package programs

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

// spawnQualifierSuffix resolves the qualifier x of a selector x.Sel through
// the file's import table: the omnipus or std package dir suffix it imports,
// or "" when x is not an imported package name.
func spawnQualifierSuffix(aliases map[string]string, x ast.Expr) string {
	if id, ok := x.(*ast.Ident); ok {
		return aliases[id.Name]
	}
	return ""
}

// spawnIsExecCtor reports whether a call is exec.Command or
// exec.CommandContext through the file's import table (an aliased os/exec is
// caught).
func spawnIsExecCtor(aliases map[string]string, call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext" {
		return false
	}
	return spawnQualifierSuffix(aliases, sel.X) == "/os/exec"
}

// spawnAttachVar returns the command variable an Attach call attaches, or ""
// when the call is not a recognised programs.Attach of a named variable.
func spawnAttachVar(aliases map[string]string, samePackage bool, call *ast.CallExpr) string {
	if len(call.Args) == 0 {
		return ""
	}
	first, ok := call.Args[0].(*ast.Ident)
	if !ok {
		return ""
	}
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		if fun.Sel.Name != "Attach" || spawnQualifierSuffix(aliases, fun.X) != "/pkg/programs" {
			return ""
		}
	case *ast.Ident:
		if fun.Name != "Attach" || !samePackage {
			return ""
		}
	default:
		return ""
	}
	return first.Name
}

// spawnIsSinkCall reports whether a call is a known child-start sink: the
// sandbox's locked starts through the import table, or a call of one of the
// table's launcher helpers, as a plain call or as a method call on a value
// (the account check starts through a method of its service).
func spawnIsSinkCall(aliases map[string]string, call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		switch spawnQualifierSuffix(aliases, fun.X) {
		case "/pkg/sandbox":
			return spawnSinkSelectors[fun.Sel.Name]
		case "":
			return spawnLocalStartWrappers[fun.Sel.Name]
		}
	case *ast.Ident:
		return spawnLocalStartWrappers[fun.Name]
	}
	return false
}

// spawnIsMandatory reports whether the table lists (file, funcName) as an S1
// mandatory site.
func spawnIsMandatory(file, funcName string) bool {
	for _, row := range spawnSiteTable {
		if row.File == file && row.Func == funcName && row.Kind == "mandatory-s1" {
			return true
		}
	}
	return false
}

// spawnLauncherNames returns the function names of the table's launcher rows:
// the helpers whose callers' hand-over of a constructed command is a launch.
func spawnLauncherNames(rows []spawnSiteRow) map[string]bool {
	names := map[string]bool{}
	for _, row := range rows {
		if row.Kind == "launcher" {
			names[row.Func] = true
		}
	}
	return names
}

// spawnIsExecCmdPtr reports whether a type expression is *exec.Cmd through
// the file's import table.
func spawnIsExecCmdPtr(aliases map[string]string, typ ast.Expr) bool {
	star, ok := typ.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Cmd" && spawnQualifierSuffix(aliases, sel.X) == "/os/exec"
}

// spawnCmdParams returns the names of a function's *exec.Cmd parameters.
func spawnCmdParams(aliases map[string]string, ft *ast.FuncType) map[string]bool {
	names := map[string]bool{}
	if ft == nil || ft.Params == nil {
		return names
	}
	for _, field := range ft.Params.List {
		if !spawnIsExecCmdPtr(aliases, field.Type) {
			continue
		}
		for _, id := range field.Names {
			if id.Name != "_" {
				names[id.Name] = true
			}
		}
	}
	return names
}

// spawnConstructedVars returns the names assigned from an exec constructor
// anywhere in a function body. The constructor pass already owns the launches
// of those names, so the parameter pass skips them and a launch is never
// counted twice (a parameter defaulted from a constructor is one example).
func spawnConstructedVars(aliases map[string]string, body *ast.BlockStmt) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != len(as.Rhs) || (as.Tok != token.DEFINE && as.Tok != token.ASSIGN) {
			return true
		}
		for i, rhs := range as.Rhs {
			call, isCall := rhs.(*ast.CallExpr)
			id, isIdent := as.Lhs[i].(*ast.Ident)
			if isCall && isIdent && spawnIsExecCtor(aliases, call) {
				out[id.Name] = true
			}
		}
		return true
	})
	return out
}

// spawnHasTokenLiteral reports whether a function body holds a string literal
// naming a program-owned variable (programTokenVarNames).
func spawnHasTokenLiteral(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		for _, name := range programTokenVarNames {
			if strings.Contains(lit.Value, name) {
				found = true
			}
		}
		return true
	})
	return found
}

// spawnParamLaunchSites finds the launches of a *exec.Cmd parameter: the
// helper-split flows CHECK R06 named, in which one function constructs and
// attaches a command and another, receiving it, starts the child. The site is
// the launch inside the receiving function and its Attach flag is judged
// inside that function, so a helper that starts a command its caller
// attached is classified by its own table row (kind "launcher") while its
// callers' sites carry the Attach check. A function that only configures a
// received command, or only constructs one, holds no launch and is no site.
func spawnParamLaunchSites(fset *token.FileSet, file string, f *ast.File, aliases map[string]string, samePackage bool) []spawnSite {
	var sites []spawnSite
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		params := spawnCmdParams(aliases, fn.Type)
		for name := range spawnConstructedVars(aliases, fn.Body) {
			delete(params, name)
		}
		if len(params) == 0 {
			continue
		}
		sites = append(sites, spawnParamSitesIn(fset, file, fn, params, aliases, samePackage)...)
	}
	return sites
}

// spawnParamSitesIn records one site per launch of a received command inside
// one function: a launch method called on it, or the command handed to a
// known sink. A programs.Attach of that command earlier in the same function
// makes the site compliant.
func spawnParamSitesIn(fset *token.FileSet, file string, fn *ast.FuncDecl, params map[string]bool, aliases map[string]string, samePackage bool) []spawnSite {
	type launch struct {
		name string
		pos  token.Pos
	}
	var launches []launch
	attaches := map[string][]token.Pos{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if v := spawnAttachVar(aliases, samePackage, call); v != "" {
			attaches[v] = append(attaches[v], call.Pos())
		}
		if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel && spawnLaunchMethods[sel.Sel.Name] {
			if id, isIdent := sel.X.(*ast.Ident); isIdent && params[id.Name] {
				launches = append(launches, launch{id.Name, call.Pos()})
			}
		}
		if spawnIsSinkCall(aliases, call) {
			for _, arg := range call.Args {
				if id, isIdent := arg.(*ast.Ident); isIdent && params[id.Name] {
					launches = append(launches, launch{id.Name, call.Pos()})
				}
			}
		}
		return true
	})
	literal := spawnHasTokenLiteral(fn.Body)
	sites := make([]spawnSite, 0, len(launches))
	for _, l := range launches {
		attached := false
		for _, a := range attaches[l.name] {
			if a < l.pos {
				attached = true
				break
			}
		}
		sites = append(sites, spawnSite{
			File:              file,
			Line:              fset.Position(l.pos).Line,
			Func:              fn.Name.Name,
			AttachSameCommand: attached,
			EnvProgramNameLit: literal,
			Mandatory:         spawnIsMandatory(file, fn.Name.Name),
		})
	}
	return sites
}

// spawnTokenReadAPI is the one function that reads a program-owned token
// (pkg/credentials/program.go::ReadProgramCredential).
const spawnTokenReadAPI = "ReadProgramCredential"

// spawnIsTokenRead reports whether a call reads a program-owned token: a call
// of the credentials package's read API through the import table, or a bare
// call inside the credentials package itself.
func spawnIsTokenRead(aliases map[string]string, inCredentials bool, call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fun.Sel.Name == spawnTokenReadAPI && spawnQualifierSuffix(aliases, fun.X) == "/pkg/credentials"
	case *ast.Ident:
		return inCredentials && fun.Name == spawnTokenReadAPI
	}
	return false
}

// spawnTokenReaders returns, per function name, whether its body reads a
// program-owned token.
func spawnTokenReaders(f *ast.File, aliases map[string]string) map[string]bool {
	reads := map[string]bool{}
	inCredentials := f.Name != nil && f.Name.Name == "credentials"
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, isCall := n.(*ast.CallExpr); isCall && spawnIsTokenRead(aliases, inCredentials, call) {
				reads[fn.Name.Name] = true
			}
			return true
		})
	}
	return reads
}

// spawnFinishSites completes one file's enumeration: it appends the launches
// of received commands and marks every site whose function reads a
// program-owned token through the credentials API.
func spawnFinishSites(fset *token.FileSet, file string, f *ast.File, aliases map[string]string, samePackage bool, sites []spawnSite) []spawnSite {
	sites = append(sites, spawnParamLaunchSites(fset, file, f, aliases, samePackage)...)
	reads := spawnTokenReaders(f, aliases)
	for i := range sites {
		sites[i].ReadsProgramToken = reads[sites[i].Func]
	}
	return sites
}

// spawnCountViolations is the per-site inventory rule (S-106/R06): a listed
// function must hold exactly the launches its row lists. A second launch in a
// listed function inherits no exemption, and a function that dropped a launch
// leaves a stale count. Functions with no discovered launch are the stale-row
// rule's concern (spawnStaleRows), so a partial scan does not trip this one.
func spawnCountViolations(sites []spawnSite) []string {
	counts := map[[2]string]int{}
	for _, s := range sites {
		counts[[2]string{s.File, s.Func}]++
	}
	var violations []string
	for _, row := range spawnSiteTable {
		n := counts[[2]string{row.File, row.Func}]
		if n == 0 || n == row.Launches {
			continue
		}
		violations = append(violations, fmt.Sprintf(
			"%s::%s holds %d child-start launches but its table row lists %d — classify each launch by its own row (S-106/R06)",
			row.File, row.Func, n, row.Launches))
	}
	return violations
}

// spawnStaleRows flags every table row that no discovered launch matches. A
// row nothing can fail on is vacuous: a mandatory row with no launch passes
// whatever the function does (CHECK R06). Run it on a FULL tree scan only.
func spawnStaleRows(sites []spawnSite) []string {
	seen := map[[2]string]bool{}
	for _, s := range sites {
		seen[[2]string{s.File, s.Func}] = true
	}
	var violations []string
	for _, row := range spawnSiteTable {
		if !seen[[2]string{row.File, row.Func}] {
			violations = append(violations, fmt.Sprintf(
				"table row %s::%s (%s) matches no discovered launch — a row no site can fail is vacuous (S-106/R06)",
				row.File, row.Func, row.Kind))
		}
	}
	return violations
}
