package sandbox

import (
	"go/ast"
	"go/token"
	"go/types"
)

func (p *wu19Inventory) nonStartingCommandHelper(key string, seen map[string]bool) bool {
	list := p.funcs[key]
	if len(list) == 0 || seen[key] {
		return false
	}
	seen[key] = true
	defer delete(seen, key)
	for _, fn := range list {
		safe := true
		ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
			if a, ok := n.(*ast.AssignStmt); ok {
				for _, lhs := range a.Lhs {
					if s, ok := lhs.(*ast.SelectorExpr); ok && s.Sel.Name == "Env" {
						safe = false
					}
				}
			}
			if c, ok := n.(*ast.CallExpr); ok {
				if s, ok := c.Fun.(*ast.SelectorExpr); ok {
					switch s.Sel.Name {
					case "Start", "Run", "Output", "CombinedOutput":
						safe = false
					}
				}
				for _, arg := range c.Args {
					if id, ok := arg.(*ast.Ident); ok && wu19ParameterIndex(fn, fn.file.object(id)) >= 0 {
						safe = safe && p.nonStartingCommandHelper(fn.callIdentity(c.Fun), seen)
					}
				}
			}
			return true
		})
		if !safe {
			return false
		}
	}
	return true
}

func (p *wu19Inventory) incomingOrigin(fn *wu19Function, index int, member string, seen map[string]bool) wu19Origin {
	key := "incoming:" + fn.key + ":" + member
	if seen[key] {
		return 0
	}
	seen[key] = true
	defer delete(seen, key)
	var out wu19Origin
	for _, list := range p.funcs {
		for _, caller := range list {
			wu19InspectReachable(caller, caller.decl.Body, seen, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || caller.callIdentity(call.Fun) != fn.key || index >= len(call.Args) {
					return true
				}
				if member == "" {
					out |= p.origin(caller, call.Args[index], call.Pos(), nil, seen)
				} else {
					out |= p.memberOrigin(caller, call.Args[index], member, call.Pos(), nil, seen)
				}
				return true
			})
		}
	}
	// An uncalled parameter is explicit input but has no scrub origin. It can
	// be added TO a rooted baseline; it cannot justify a whole command Env.
	return out
}

func (p *wu19Inventory) memberOrigin(fn *wu19Function, base ast.Expr, member string, before token.Pos, bindings map[types.Object]wu19Reference, seen map[string]bool) wu19Origin {
	if typ := p.declaredType(fn, &ast.SelectorExpr{X: base, Sel: ast.NewIdent(member)}); typ != nil {
		if scalar, ok := typ.(*ast.Ident); ok {
			switch scalar.Name {
			case "string", "int", "int32", "int64", "uint16", "bool":
				return 0 // declared scalar configuration/path addition, not a whole environment.
			}
		}
	}
	switch e := base.(type) {
	case *ast.ParenExpr:
		return p.memberOrigin(fn, e.X, member, before, bindings, seen)
	case *ast.UnaryExpr:
		return p.memberOrigin(fn, e.X, member, before, bindings, seen)
	case *ast.CompositeLit:
		for _, entry := range e.Elts {
			if kv, ok := entry.(*ast.KeyValueExpr); ok {
				if name, ok := kv.Key.(*ast.Ident); ok && name.Name == member {
					return p.origin(fn, kv.Value, before, bindings, seen)
				}
			}
		}
		return 0
	case *ast.Ident:
		var fields wu19Origin
		found := false
		wu19InspectReachable(fn, fn.decl.Body, seen, func(n ast.Node) bool {
			if a, ok := n.(*ast.AssignStmt); ok && a.Pos() < before && len(a.Lhs) == 1 && len(a.Rhs) == 1 {
				if s, ok := a.Lhs[0].(*ast.SelectorExpr); ok && s.Sel.Name == member {
					if x, ok := s.X.(*ast.Ident); ok && fn.file.object(x) == fn.file.object(e) {
						found = true
						fields |= p.origin(fn, a.Rhs[0], a.Pos(), bindings, seen)
					}
				}
			}
			return true
		})
		if found {
			return fields
		}
		if ref, ok := bindings[fn.file.object(e)]; ok {
			return p.memberOrigin(ref.fn, ref.expr, member, ref.pos, ref.bindings, seen)
		}
		if index := wu19ParameterIndex(fn, fn.file.object(e)); index >= 0 {
			if fn.file.pkg == "github.com/elicify-ai/omnipus/pkg/agent/runner" && member == "Env" {
				// External RunOptions.Env is the documented incoming credential
				// frontier. Pin its gateway producer to the runner scrub exactly;
				// do not silently bless an unknown or generic environment class.
				return p.runnerDispatchOrigin(seen)
			}
			return p.incomingOrigin(fn, index, member, seen)
		}
		var out wu19Origin
		for _, value := range wu19Definitions(fn, fn.file.object(e), before, seen) {
			out |= p.memberOrigin(fn, value, member, before, bindings, seen)
		}
		return out
	case *ast.CallExpr:
		var out wu19Origin
		for _, callee := range p.funcs[fn.callIdentity(e.Fun)] {
			args := map[types.Object]wu19Reference{}
			for i, parameter := range wu19Parameters(callee) {
				if i < len(e.Args) {
					args[callee.file.object(parameter)] = wu19Reference{fn, e.Args[i], before, bindings}
				}
			}
			ast.Inspect(callee.decl.Body, func(n ast.Node) bool {
				if ret, ok := n.(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
					out |= p.memberOrigin(callee, ret.Results[0], member, ret.Pos(), args, seen)
				}
				return true
			})
		}
		return out
	}
	// Struct/configuration members are site-declared data, not implicit parent
	// inheritance. They add no scrub root. Raw helper calls are still followed.
	return 0
}

func (p *wu19Inventory) runnerDispatchOrigin(seen map[string]bool) wu19Origin {
	var out wu19Origin
	count := 0
	for _, list := range p.funcs {
		for _, fn := range list {
			if fn.file.pkg != "github.com/elicify-ai/omnipus/pkg/agent" {
				continue
			}
			ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
				a, ok := n.(*ast.AssignStmt)
				if !ok || len(a.Lhs) != 1 || len(a.Rhs) != 1 {
					return true
				}
				field, ok := a.Lhs[0].(*ast.SelectorExpr)
				if !ok || field.Sel.Name != "childEnv" {
					return true
				}
				count++
				call, ok := a.Rhs[0].(*ast.CallExpr)
				if !ok {
					out |= wu19Bad
					return true
				}
				// Later, site-declared proxy augmentation must still trace back
				// through the preceding childEnv assignment to the runner scrub.
				out |= p.origin(fn, call, a.Pos(), nil, seen)
				return true
			})
		}
	}
	if count == 0 || out&wu19Runner == 0 {
		out |= wu19Bad
	}
	return out
}
