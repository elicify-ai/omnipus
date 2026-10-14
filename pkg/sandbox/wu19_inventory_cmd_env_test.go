package sandbox

import (
	"fmt"
	"go/ast"
	"go/token"
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

func (p *wu19Inventory) commandEnvWriteSafe(fn *wu19Function, field *ast.SelectorExpr, value ast.Expr, partial bool, before token.Pos, bindings map[*ast.Object]wu19Reference, trace wu19CommandTrace) bool {
	kind := p.commandIdentity(fn, field.X, before, bindings, trace, map[string]bool{})
	if kind == wu19OtherValue {
		return true // a known non-command configuration field is not this Env.
	}
	if kind != wu19TrackedCommand {
		return false
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
func (p *wu19Inventory) commandEnvOrigin(fn *wu19Function, expr ast.Expr, before token.Pos, bindings map[*ast.Object]wu19Reference, trace wu19CommandTrace) wu19Origin {
	return p.commandEnvValue(fn, expr, before, bindings, trace, map[string]bool{})
}

func (p *wu19Inventory) commandEnvValue(fn *wu19Function, expr ast.Expr, before token.Pos, bindings map[*ast.Object]wu19Reference, trace wu19CommandTrace, seen map[string]bool) wu19Origin {
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
		key := fmt.Sprintf("%s:%p:%d", fn.key, e.Obj, before)
		if seen[key] {
			return 0 // append-self adds no new provenance; other operands are checked.
		}
		seen[key] = true
		defer delete(seen, key)
		if ref, ok := bindings[e.Obj]; ok {
			return p.commandEnvValue(ref.fn, ref.expr, ref.pos, ref.bindings, trace, seen)
		}
		if refs := wu19CommandDefinitions(fn, e.Obj, before); len(refs) > 0 {
			var out wu19Origin
			for _, ref := range refs {
				out |= p.commandEnvValue(fn, ref.expr, ref.pos, bindings, trace, seen)
			}
			return out
		}
	case *ast.CallExpr:
		if builtin, ok := e.Fun.(*ast.Ident); ok && builtin.Name == "append" && builtin.Obj == nil {
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
