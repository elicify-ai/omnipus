package sandbox

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
)

// Whole-slice, element and copy writes use the same pointer/provenance rules.
func wu19CommandEnvTarget(expr ast.Expr) (*ast.SelectorExpr, bool) {
	partial := false
	for {
		switch e := expr.(type) {
		case *ast.ParenExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr, partial = e.X, true
		case *ast.SliceExpr:
			expr, partial = e.X, true
		case *ast.SelectorExpr:
			if e.Sel.Name == "Env" {
				return e, partial
			}
			return nil, false
		default:
			return nil, false
		}
	}
}

func (p *wu19Inventory) commandEnvWriteSafe(fn *wu19Function, field *ast.SelectorExpr, value ast.Expr, partial bool, before token.Pos, bindings map[types.Object]wu19Reference, trace wu19CommandTrace) bool {
	kind := p.commandIdentity(fn, field.X, before, bindings, trace, map[string]bool{})
	if kind == wu19OtherValue {
		return true // a known non-command configuration field is not this Env.
	}
	if kind != wu19TrackedCommand {
		return false
	}
	if !partial && p.envAppendKeepsRoot(fn, field, value, before, bindings, trace) {
		return true // ruling WU19-win 1b: the command keeps its existing root unchanged.
	}
	origin := p.commandEnvOrigin(fn, value, before, bindings, trace)
	if partial {
		// An explicit element/copy retains the command's already rooted slice.
		root := trace.env
		origin |= p.origin(root.fn, root.expr, root.pos, root.bindings, map[string]bool{})
	}
	return origin&(wu19Bad|wu19MayBeNil|wu19GodException) == 0 && origin&(wu19Generic|wu19Runner) != 0
}

// Env reads through any proven command alias carry the original scrub root.
// Environment data from an unproved command alias is bad, not an explicit site
// addition. Other expressions retain the existing import-resolved provenance.
func (p *wu19Inventory) commandEnvOrigin(fn *wu19Function, expr ast.Expr, before token.Pos, bindings map[types.Object]wu19Reference, trace wu19CommandTrace) wu19Origin {
	return p.commandEnvValue(fn, expr, before, bindings, trace, map[string]bool{})
}

func (p *wu19Inventory) commandEnvValue(fn *wu19Function, expr ast.Expr, before token.Pos, bindings map[types.Object]wu19Reference, trace wu19CommandTrace, seen map[string]bool) wu19Origin {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		if e.Sel.Name == "Env" {
			kind := p.commandIdentity(fn, e.X, before, bindings, trace, map[string]bool{})
			if kind == wu19TrackedCommand {
				root := trace.env
				return p.origin(root.fn, root.expr, root.pos, root.bindings, map[string]bool{})
			}
			if kind != wu19OtherValue {
				return wu19Bad
			}
		}
	case *ast.Ident:
		key := fmt.Sprintf("%s:%p:%d", fn.key, fn.file.object(e), before)
		if seen[key] {
			return 0 // append-self adds no new provenance; other operands are checked.
		}
		seen[key] = true
		defer delete(seen, key)
		var out wu19Origin
		found := false
		if ref, ok := bindings[fn.file.object(e)]; ok {
			found = true
			out |= p.commandEnvValue(ref.fn, ref.expr, ref.pos, ref.bindings, trace, seen)
		}
		for _, ref := range wu19CommandDefinitions(fn, fn.file.object(e), before) {
			found = true
			out |= p.commandEnvValue(fn, ref.expr, ref.pos, bindings, trace, seen)
		}
		if found {
			return out
		}
	case *ast.CallExpr:
		if builtin, ok := e.Fun.(*ast.Ident); ok && builtin.Name == "append" && fn.file.object(builtin) == nil {
			var out wu19Origin
			for _, arg := range e.Args {
				out |= p.commandEnvValue(fn, arg, before, bindings, trace, seen)
			}
			return out &^ wu19MayBeNil
		}
	case *ast.ParenExpr:
		return p.commandEnvValue(fn, e.X, before, bindings, trace, seen)
	case *ast.UnaryExpr:
		return p.commandEnvValue(fn, e.X, before, bindings, trace, seen)
	case *ast.BinaryExpr:
		return p.commandEnvValue(fn, e.X, before, bindings, trace, seen) | p.commandEnvValue(fn, e.Y, before, bindings, trace, seen)
	case *ast.SliceExpr:
		return p.commandEnvValue(fn, e.X, before, bindings, trace, seen)
	case *ast.IndexExpr:
		return p.commandEnvValue(fn, e.X, before, bindings, trace, seen)
	case *ast.CompositeLit:
		var out wu19Origin
		for _, entry := range e.Elts {
			if pair, ok := entry.(*ast.KeyValueExpr); ok {
				entry = pair.Value
			}
			out |= p.commandEnvValue(fn, entry, before, bindings, trace, seen)
		}
		return out
	}
	return p.origin(fn, expr, before, bindings, map[string]bool{})
}

// envAppendKeepsRoot is the one append rule of ruling WU19-win 1b. A helper's
// write to the traced command's Env is safe without re-deriving a root only
// when ALL of these hold:
//  1. the right-hand side calls the real builtin append (not shadowed by a
//     local or package-level append);
//  2. its first argument is exactly <c>.Env for the SAME tracked command (not
//     a slice expression, another command or an unproved alias);
//  3. no other argument carries environment data of any kind: its origin has
//     none of wu19Bad, wu19MayBeNil, wu19GodException, wu19Generic or
//     wu19Runner;
//  4. the helper writes that command's Env nowhere else (no nil fallback or
//     other assignment, copy or element write, reachable or not).
//
// Such a write keeps the command's existing root, including a caller's God
// Mode exception; it never creates or grants one.
func (p *wu19Inventory) envAppendKeepsRoot(fn *wu19Function, field *ast.SelectorExpr, value ast.Expr, before token.Pos, bindings map[types.Object]wu19Reference, trace wu19CommandTrace) bool {
	call, ok := value.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return false
	}
	builtin, ok := call.Fun.(*ast.Ident)
	if !ok || builtin.Name != "append" || fn.file.object(builtin) != nil {
		return false
	}
	if _, shadowed := p.funcs[fn.file.pkg+".append"]; shadowed {
		return false
	}
	if _, shadowed := p.globals[fn.file.pkg+".append"]; shadowed {
		return false
	}
	first, ok := call.Args[0].(*ast.SelectorExpr)
	if !ok || first.Sel.Name != "Env" || p.commandIdentity(fn, first.X, before, bindings, trace, map[string]bool{}) != wu19TrackedCommand {
		return false
	}
	const envData = wu19Bad | wu19MayBeNil | wu19GodException | wu19Generic | wu19Runner
	for _, arg := range call.Args[1:] {
		if p.commandEnvValue(fn, arg, before, bindings, trace, map[string]bool{})&envData != 0 {
			return false
		}
	}
	only := true
	ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
		var targets []ast.Expr
		var at token.Pos
		switch node := n.(type) {
		case *ast.AssignStmt:
			if len(node.Rhs) == 1 && node.Rhs[0] == value {
				return true // the append write itself.
			}
			targets, at = node.Lhs, node.Pos()
		case *ast.CallExpr:
			if id, ok := node.Fun.(*ast.Ident); ok && id.Name == "copy" && fn.file.object(id) == nil && len(node.Args) == 2 {
				targets, at = node.Args[:1], node.Pos()
			}
		}
		for _, target := range targets {
			if other, _ := wu19CommandEnvTarget(target); other != nil && p.commandIdentity(fn, other.X, at, bindings, trace, map[string]bool{}) != wu19OtherValue {
				only = false
			}
		}
		return true
	})
	return only
}
