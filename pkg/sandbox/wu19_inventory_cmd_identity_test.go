package sandbox

import (
	"fmt"
	"go/ast"
	"go/token"
)

// Identity is a conservative union of every prior definition, including branch
// alternatives. A tracked pointer mixed with nil/opaque/another pointer is not
// proven to be the command. It must never silently become an unrelated value.
type wu19CommandIdentity uint8

const (
	wu19TrackedCommand wu19CommandIdentity = 1 << iota
	wu19OtherValue
	wu19UnprovedCommand
)

type wu19CommandTrace struct {
	root *ast.Object
	env  wu19Reference
}

func (p *wu19Inventory) commandIdentity(fn *wu19Function, expr ast.Expr, before token.Pos, bindings map[*ast.Object]wu19Reference, trace wu19CommandTrace, seen map[string]bool) wu19CommandIdentity {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return p.commandIdentity(fn, e.X, before, bindings, trace, seen)
	case *ast.Ident:
		if e.Obj == trace.root && e.Obj != nil {
			return wu19TrackedCommand
		}
		key := fmt.Sprintf("%s:%p:%d", fn.key, e.Obj, before)
		if seen[key] {
			return wu19UnprovedCommand
		}
		seen[key] = true
		defer delete(seen, key)
		var out wu19CommandIdentity
		if ref, ok := bindings[e.Obj]; ok {
			out |= p.commandIdentity(ref.fn, ref.expr, ref.pos, ref.bindings, trace, seen)
		}
		for _, ref := range wu19CommandDefinitions(fn, e.Obj, before) {
			out |= p.commandIdentity(ref.fn, ref.expr, ref.pos, bindings, trace, seen)
		}
		if out != 0 {
			return out
		}
		if e.Name == "nil" {
			return wu19UnprovedCommand
		}
		if typ := p.declaredType(fn, e); typ != nil && p.ownedTypeIdentity(fn, typ) != "os/exec.Cmd" {
			return wu19OtherValue // a declared non-command input, not an inference gap.
		}
		if e.Name == "true" || e.Name == "false" || (e.Obj == nil && fn.file.imports[e.Name] != "") {
			return wu19OtherValue
		}
		return wu19UnprovedCommand
	case *ast.SelectorExpr:
		base := p.commandIdentity(fn, e.X, before, bindings, trace, seen)
		if base == wu19TrackedCommand {
			return wu19OtherValue // exec.Cmd attributes are not command pointers.
		}
		if e.Sel.Name == "Env" && base&wu19TrackedCommand != 0 {
			return wu19OtherValue // environment data is checked by the provenance leg.
		}
		refs := p.commandMemberDefinitions(fn, e.X, e.Sel.Name, before, bindings, map[string]bool{})
		var out wu19CommandIdentity
		for _, ref := range refs {
			out |= p.commandIdentity(ref.fn, ref.expr, ref.pos, ref.bindings, trace, seen)
		}
		if out != 0 {
			return out
		}
		if typ := p.declaredType(fn, e); typ != nil && p.ownedTypeIdentity(fn, typ) != "os/exec.Cmd" {
			return wu19OtherValue
		}
		return wu19UnprovedCommand // an unresolved command-bearing field is not safe.
	case *ast.CallExpr:
		key := p.resolvedCallIdentity(fn, e.Fun)
		list := p.funcs[key]
		if len(list) > 0 && list[0].decl.Type.Results != nil && len(list[0].decl.Type.Results.List) > 0 {
			if p.ownedTypeIdentity(list[0], list[0].decl.Type.Results.List[0].Type) != "os/exec.Cmd" {
				return wu19OtherValue
			}
		}
		// Opaque pointer-producing expressions are not blessed by a helper name.
		return wu19UnprovedCommand
	case *ast.CompositeLit:
		var out wu19CommandIdentity
		for _, entry := range e.Elts {
			if pair, ok := entry.(*ast.KeyValueExpr); ok {
				entry = pair.Value
			}
			out |= p.commandIdentity(fn, entry, before, bindings, trace, seen)
		}
		if out&wu19TrackedCommand != 0 {
			return wu19UnprovedCommand | wu19TrackedCommand // aggregate escape needs its own binding, not a pointer waiver.
		}
		return wu19OtherValue
	case *ast.UnaryExpr:
		return p.commandIdentity(fn, e.X, before, bindings, trace, seen)
	case *ast.StarExpr:
		return p.commandIdentity(fn, e.X, before, bindings, trace, seen)
	case *ast.IndexExpr, *ast.TypeAssertExpr:
		return wu19UnprovedCommand // dynamic selection is not a proven pointer alias.
	case *ast.BasicLit:
		return wu19OtherValue
	case *ast.BadExpr:
		return wu19UnprovedCommand
	}
	return wu19UnprovedCommand
}

// A declared command-pointer slot is a handoff even when its argument's local
// initializer is opaque. Consult both sides; inference failure is not a waiver.
func (p *wu19Inventory) commandArgumentType(fn *wu19Function, call *ast.CallExpr, index int, arg ast.Expr) bool {
	if p.ownedTypeIdentity(fn, p.declaredType(fn, arg)) == "os/exec.Cmd" {
		return true
	}
	for _, callee := range p.funcs[p.resolvedCallIdentity(fn, call.Fun)] {
		position := 0
		if callee.decl.Type.Params == nil {
			continue
		}
		for _, field := range callee.decl.Type.Params.List {
			count := len(field.Names)
			if count == 0 {
				count = 1
			}
			if index >= position && index < position+count && p.ownedTypeIdentity(callee, field.Type) == "os/exec.Cmd" {
				return true
			}
			position += count
		}
	}
	return false
}

// Keep the position of each definition, so a later reassignment of a source
// cannot retroactively change a pointer already copied into another local.
func wu19CommandDefinitions(fn *wu19Function, object *ast.Object, before token.Pos) []wu19Reference {
	var refs []wu19Reference
	if object == nil {
		return refs
	}
	ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
		if n == nil || n.Pos() >= before {
			return n == nil
		}
		switch node := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range node.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Obj == object {
					if len(node.Lhs) == len(node.Rhs) {
						refs = append(refs, wu19Reference{fn, node.Rhs[i], node.Pos(), nil})
					} else {
						refs = append(refs, wu19Reference{fn, &ast.BadExpr{}, node.Pos(), nil})
					}
				}
			}
		case *ast.ValueSpec:
			for i, name := range node.Names {
				if name.Obj == object && i < len(node.Values) {
					refs = append(refs, wu19Reference{fn, node.Values[i], node.Pos(), nil})
				}
			}
		}
		return true
	})
	return refs
}

// Resolve a field through parameter bindings, local copies, keyed/positional
// literals and explicit field writes. Nested selectors reuse this same resolver.
func (p *wu19Inventory) commandMemberDefinitions(fn *wu19Function, base ast.Expr, member string, before token.Pos, bindings map[*ast.Object]wu19Reference, seen map[string]bool) []wu19Reference {
	switch e := base.(type) {
	case *ast.ParenExpr:
		return p.commandMemberDefinitions(fn, e.X, member, before, bindings, seen)
	case *ast.UnaryExpr:
		return p.commandMemberDefinitions(fn, e.X, member, before, bindings, seen)
	case *ast.CompositeLit:
		for i, entry := range e.Elts {
			if pair, ok := entry.(*ast.KeyValueExpr); ok {
				if name, ok := pair.Key.(*ast.Ident); ok && name.Name == member {
					return []wu19Reference{{fn, pair.Value, before, bindings}}
				}
			} else if p.commandLiteralField(fn, e.Type, i) == member {
				return []wu19Reference{{fn, entry, before, bindings}}
			}
		}
	case *ast.Ident:
		key := fmt.Sprintf("%s:%p:%s:%d", fn.key, e.Obj, member, before)
		if seen[key] {
			return []wu19Reference{{fn, &ast.BadExpr{}, before, bindings}}
		}
		seen[key] = true
		defer delete(seen, key)
		var refs []wu19Reference
		if ref, ok := bindings[e.Obj]; ok {
			refs = append(refs, p.commandMemberDefinitions(ref.fn, ref.expr, member, ref.pos, ref.bindings, seen)...)
		}
		for _, ref := range wu19CommandDefinitions(fn, e.Obj, before) {
			refs = append(refs, p.commandMemberDefinitions(fn, ref.expr, member, ref.pos, bindings, seen)...)
		}
		ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
			if assignment, ok := n.(*ast.AssignStmt); ok && assignment.Pos() < before && len(assignment.Lhs) == len(assignment.Rhs) {
				for i, lhs := range assignment.Lhs {
					if field, ok := lhs.(*ast.SelectorExpr); ok && field.Sel.Name == member {
						if id, ok := field.X.(*ast.Ident); ok && id.Obj == e.Obj {
							refs = append(refs, wu19Reference{fn, assignment.Rhs[i], assignment.Pos(), bindings})
						}
					}
				}
			}
			return true
		})
		return refs
	case *ast.SelectorExpr:
		var refs []wu19Reference
		for _, ref := range p.commandMemberDefinitions(fn, e.X, e.Sel.Name, before, bindings, seen) {
			refs = append(refs, p.commandMemberDefinitions(ref.fn, ref.expr, member, ref.pos, ref.bindings, seen)...)
		}
		return refs
	}
	return nil
}

func (p *wu19Inventory) commandLiteralField(fn *wu19Function, typ ast.Expr, index int) string {
	structure, ok := typ.(*ast.StructType)
	if !ok {
		structure, ok = p.types[p.ownedTypeIdentity(fn, typ)].(*ast.StructType)
	}
	if !ok {
		return ""
	}
	var names []string
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
	}
	if index < len(names) {
		return names[index]
	}
	return ""
}
