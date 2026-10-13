package sandbox

import "go/ast"

// A helper called after the initial Env assignment may not reopen inheritance.
// Inspect actual helper bodies; no method-name allowlist blesses a raw mutator.
func (p *wu19Inventory) commandMutationSafe(caller *wu19Function, call *ast.CallExpr, command *ast.Object, seen map[string]bool) bool {
	key := p.resolvedCallIdentity(caller, call.Fun)
	if seen[key] {
		return true
	}
	list := p.funcs[key]
	if len(list) == 0 {
		if key == "github.com/elicify-ai/omnipus/pkg/tools/browser/cdppipe.PipeOptions.ModifyCmd" {
			return p.browserModifierCallbacksPreserveEnv()
		}
		p.unresolved["command helper: "+key+" in "+caller.key] = true
		return false // unknown command-pointer handoff is not a verified mutation boundary.
	}
	seen[key] = true
	defer delete(seen, key)
	for _, callee := range list {
		bindings := map[*ast.Object]wu19Reference{}
		commandParams := map[*ast.Object]bool{}
		for i, parameter := range wu19Parameters(callee) {
			if i < len(call.Args) {
				bindings[parameter.Obj] = wu19Reference{caller, call.Args[i], call.Pos(), nil}
				commandParams[parameter.Obj] = wu19SameCommand(call.Args[i], command, caller.decl.Body, map[*ast.Object]bool{})
			}
		}
		safe := true
		ast.Inspect(callee.decl.Body, func(n ast.Node) bool {
			if conditional, ok := n.(*ast.IfStmt); ok && wu19NilCommandEnvGuard(conditional.Cond, commandParams) {
				// The caller's rooted, unconditional Env assignment is known
				// non-nil; this fallback materialization branch cannot run.
				return false
			}
			if assignment, ok := n.(*ast.AssignStmt); ok && len(assignment.Lhs) == len(assignment.Rhs) {
				for i, lhs := range assignment.Lhs {
					field, ok := lhs.(*ast.SelectorExpr)
					if !ok || field.Sel.Name != "Env" {
						continue
					}
					param, ok := field.X.(*ast.Ident)
					if !ok || !commandParams[param.Obj] {
						continue
					}
					origin := p.origin(callee, assignment.Rhs[i], assignment.Pos(), bindings, map[string]bool{})
					// The God exception belongs to exactly three constructor
					// assignments, not to an arbitrary command-pointer mutator.
					if origin&(wu19Bad|wu19MayBeNil|wu19GodException) != 0 || origin&(wu19Generic|wu19Runner) == 0 {
						safe = false
					}
				}
			}
			if nested, ok := n.(*ast.CallExpr); ok {
				for _, arg := range nested.Args {
					if id, ok := arg.(*ast.Ident); ok && commandParams[id.Obj] {
						safe = safe && p.commandMutationSafe(callee, nested, id.Obj, seen)
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

func wu19NilCommandEnvGuard(expr ast.Expr, commandParams map[*ast.Object]bool) bool {
	condition, ok := expr.(*ast.BinaryExpr)
	if !ok || condition.Op.String() != "==" {
		return false
	}
	field, ok := condition.X.(*ast.SelectorExpr)
	if !ok || field.Sel.Name != "Env" {
		return false
	}
	command, ok := field.X.(*ast.Ident)
	if !ok || !commandParams[command.Obj] {
		return false
	}
	nilValue, ok := condition.Y.(*ast.Ident)
	return ok && nilValue.Name == "nil"
}
