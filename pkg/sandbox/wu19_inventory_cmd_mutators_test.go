package sandbox

import (
	"go/ast"
	"go/token"
	"go/types"
)

// A helper called after the initial Env assignment may not reopen inheritance.
// One identity trace follows locals, fields and nested parameter bindings; no
// local name or helper name can grant a fourth God environment exception.
func (p *wu19Inventory) commandMutationSafe(caller *wu19Function, call *ast.CallExpr, command types.Object, seen map[string]bool) bool {
	root := ast.NewIdent(command.Name())
	caller.file.identities.Uses[root] = command
	trace := wu19CommandTrace{command, wu19Reference{caller, &ast.SelectorExpr{X: root, Sel: ast.NewIdent("Env")}, call.Pos(), nil}}
	return p.commandCallSafe(caller, call, nil, trace, seen)
}

func (p *wu19Inventory) commandCallSafe(caller *wu19Function, call *ast.CallExpr, callerBindings map[types.Object]wu19Reference, trace wu19CommandTrace, seen map[string]bool) bool {
	key := p.resolvedCallIdentity(caller, call.Fun)
	if seen[key] {
		p.unresolved["recursive command handoff: "+key] = true
		return false // an unproved cycle is not a safe mutation boundary.
	}
	list := p.funcs[key]
	if len(list) == 0 {
		if key == "github.com/elicify-ai/omnipus/pkg/tools/browser/cdppipe.PipeOptions.ModifyCmd" {
			return p.browserModifierCallbacksPreserveEnv()
		}
		p.unresolved["command helper: "+key+" in "+caller.key] = true
		return false
	}
	seen[key] = true
	defer delete(seen, key)
	for _, callee := range list {
		bindings := map[types.Object]wu19Reference{}
		for i, parameter := range wu19Parameters(callee) {
			if i < len(call.Args) {
				bindings[callee.file.object(parameter)] = wu19Reference{caller, call.Args[i], call.Pos(), callerBindings}
			}
		}
		if !p.commandBodySafe(callee, bindings, trace, seen) {
			return false
		}
	}
	return true
}

func (p *wu19Inventory) commandBodySafe(fn *wu19Function, bindings map[types.Object]wu19Reference, trace wu19CommandTrace, seen map[string]bool) bool {
	safe := true
	identity := func(expr ast.Expr, before token.Pos) wu19CommandIdentity {
		return p.commandIdentity(fn, expr, before, bindings, trace, map[string]bool{})
	}
	var visit func(ast.Node) bool
	visit = func(n ast.Node) bool {
		if conditional, ok := n.(*ast.IfStmt); ok && wu19NilCommandEnvGuard(conditional.Cond, conditional.Pos(), identity) {
			// A rooted Env is non-nil. Only its nil fallback is unreachable;
			// the else branch must still be checked, including through aliases.
			if conditional.Else != nil {
				ast.Inspect(conditional.Else, visit)
			}
			return false
		}
		if assignment, ok := n.(*ast.AssignStmt); ok {
			for i, lhs := range assignment.Lhs {
				field, partial := wu19CommandEnvTarget(lhs)
				if field == nil {
					continue
				}
				if len(assignment.Lhs) != len(assignment.Rhs) {
					safe = false
					continue
				}
				if !p.commandEnvWriteSafe(fn, field, assignment.Rhs[i], partial, assignment.Pos(), bindings, trace) {
					safe = false
				}
			}
		}
		if nested, ok := n.(*ast.CallExpr); ok {
			if builtin, ok := nested.Fun.(*ast.Ident); ok && fn.file.object(builtin) == nil && builtin.Name == "copy" && len(nested.Args) == 2 {
				if field, _ := wu19CommandEnvTarget(nested.Args[0]); field != nil && !p.commandEnvWriteSafe(fn, field, nested.Args[1], true, nested.Pos(), bindings, trace) {
					safe = false
				}
			}
			for index, arg := range nested.Args {
				kind := identity(arg, nested.Pos())
				if kind&wu19TrackedCommand == 0 {
					if kind&wu19UnprovedCommand != 0 && p.commandArgumentType(fn, nested, index, arg) {
						safe = false
					}
					continue // scalar/environment arguments are not pointer handoffs.
				}
				if kind != wu19TrackedCommand || !p.commandCallSafe(fn, nested, bindings, trace, seen) {
					safe = false
				}
			}
		}
		return true
	}
	ast.Inspect(fn.decl.Body, visit)
	return safe
}

// The existing allocator callback is an explicit attribute-only handoff. Inspect
// every actual production callback definition rather than trust its name. A new
// opaque callback or one assigning Env invalidates this contract.
func (p *wu19Inventory) browserModifierCallbacksPreserveEnv() bool {
	count, safe := 0, true
	for _, file := range p.files {
		ast.Inspect(file.ast, func(n ast.Node) bool {
			var value ast.Expr
			switch node := n.(type) {
			case *ast.KeyValueExpr:
				if key, ok := node.Key.(*ast.Ident); ok && key.Name == "ModifyCmd" {
					value = node.Value
				}
			case *ast.AssignStmt:
				if len(node.Lhs) == 1 && len(node.Rhs) == 1 {
					if field, ok := node.Lhs[0].(*ast.SelectorExpr); ok && field.Sel.Name == "ModifyCmd" {
						value = node.Rhs[0]
					}
				}
			}
			if value == nil {
				return true
			}
			count++
			callback, ok := value.(*ast.FuncLit)
			if !ok {
				safe = false
				return true
			}
			ast.Inspect(callback.Body, func(inner ast.Node) bool {
				if assignment, ok := inner.(*ast.AssignStmt); ok {
					for _, lhs := range assignment.Lhs {
						if field, ok := lhs.(*ast.SelectorExpr); ok && field.Sel.Name == "Env" {
							safe = false
						}
					}
				}
				return true
			})
			return true
		})
	}
	return safe && count > 0
}

func wu19NilCommandEnvGuard(expr ast.Expr, before token.Pos, identity func(ast.Expr, token.Pos) wu19CommandIdentity) bool {
	condition, ok := expr.(*ast.BinaryExpr)
	if !ok || condition.Op != token.EQL {
		return false
	}
	field, ok := condition.X.(*ast.SelectorExpr)
	if !ok || field.Sel.Name != "Env" || identity(field.X, before) != wu19TrackedCommand {
		return false
	}
	nilValue, ok := condition.Y.(*ast.Ident)
	return ok && nilValue.Name == "nil"
}
