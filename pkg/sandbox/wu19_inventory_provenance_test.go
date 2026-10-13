package sandbox

import (
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"strings"
)

type wu19Origin uint8

const (
	wu19Generic wu19Origin = 1 << iota
	wu19Runner
	wu19Bad
	wu19MayBeNil
	wu19GodException
)

type wu19Reference struct {
	fn       *wu19Function
	expr     ast.Expr
	pos      token.Pos
	bindings map[*ast.Object]wu19Reference
}

func (p *wu19Inventory) origin(fn *wu19Function, expr ast.Expr, before token.Pos, bindings map[*ast.Object]wu19Reference, seen map[string]bool) wu19Origin {
	switch e := expr.(type) {
	case nil, *ast.BasicLit, *ast.FuncLit:
		return 0
	case *ast.ParenExpr:
		return p.origin(fn, e.X, before, bindings, seen)
	case *ast.UnaryExpr:
		return p.origin(fn, e.X, before, bindings, seen)
	case *ast.BinaryExpr:
		return p.origin(fn, e.X, before, bindings, seen) | p.origin(fn, e.Y, before, bindings, seen)
	case *ast.SliceExpr:
		return p.origin(fn, e.X, before, bindings, seen)
	case *ast.IndexExpr:
		return p.origin(fn, e.X, before, bindings, seen)
	case *ast.CompositeLit:
		var out wu19Origin
		for _, item := range e.Elts {
			if kv, ok := item.(*ast.KeyValueExpr); ok {
				item = kv.Value
			}
			out |= p.origin(fn, item, before, bindings, seen)
		}
		return out
	case *ast.SelectorExpr:
		return p.memberOrigin(fn, e.X, e.Sel.Name, before, bindings, seen)
	case *ast.Ident:
		if e.Name == "nil" {
			return wu19MayBeNil
		}
		if e.Name == "true" || e.Name == "false" {
			return 0
		}
		key := fn.key + ":" + e.Name
		if e.Obj != nil {
			key += ":" + string(rune(e.Obj.Pos()))
		}
		if seen[key] {
			return 0 // append-self cycle adds no origin; other RHS dependencies are still checked.
		}
		seen[key] = true
		defer delete(seen, key)
		if global, ok := p.globals[fn.file.pkg+"."+e.Name]; ok && (e.Obj == nil || p.globalObjects[e.Obj]) {
			return p.origin(global.fn, global.expr, global.pos, nil, seen)
		}
		if wu19ScalarParameter(fn, e.Obj) {
			return 0 // explicit key/value/path argument, not an entire environment source.
		}
		if ref, ok := bindings[e.Obj]; ok {
			return p.origin(ref.fn, ref.expr, ref.pos, ref.bindings, seen)
		}
		values := wu19Definitions(fn, e.Obj, before, seen)
		if len(values) > 0 {
			var out wu19Origin
			for _, value := range values {
				out |= p.origin(fn, value, before, bindings, seen)
			}
			return out
		}
		if index := wu19ParameterIndex(fn, e.Obj); index >= 0 {
			if wu19EnvironmentParameter(fn, e.Obj) {
				return p.incomingOrigin(fn, index, "", seen)
			}
			return 0 // scalar/configuration input is an explicit site value, not an inherited environment.
		}
		if p.scalarTupleVariable(fn, e.Obj) {
			return 0
		}
		p.unresolved[fmt.Sprintf("identifier %s in %s", e.Name, fn.key)] = true
		return wu19Bad
	case *ast.CallExpr:
		return p.callOrigin(fn, e, before, bindings, seen)
	}
	return wu19Bad
}

func wu19Definitions(fn *wu19Function, object *ast.Object, before token.Pos, facts map[string]bool) []ast.Expr {
	var values []ast.Expr
	wu19InspectReachable(fn, fn.decl.Body, facts, func(n ast.Node) bool {
		if n == nil || n.Pos() >= before {
			return n == nil
		}
		switch node := n.(type) {
		case *ast.AssignStmt:
			if len(node.Lhs) == len(node.Rhs) {
				for i, lhs := range node.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Obj == object {
						values = append(values, node.Rhs[i])
					}
					if idx, ok := lhs.(*ast.IndexExpr); ok {
						if id, ok := idx.X.(*ast.Ident); ok && id.Obj == object {
							values = append(values, node.Rhs[i])
						}
					}
				}
			} else if len(node.Rhs) == 1 {
				if id, ok := node.Lhs[0].(*ast.Ident); ok && id.Obj == object {
					values = append(values, node.Rhs[0])
				}
			}
		case *ast.ValueSpec:
			for i, name := range node.Names {
				if name.Obj == object && i < len(node.Values) {
					values = append(values, node.Values[i])
				}
			}
		case *ast.CallExpr:
			if id, ok := node.Fun.(*ast.Ident); ok && id.Name == "copy" && id.Obj == nil && len(node.Args) == 2 {
				if dst, ok := node.Args[0].(*ast.Ident); ok && dst.Obj == object {
					values = append(values, node.Args[1])
				}
			}
		case *ast.RangeStmt:
			for _, variable := range []ast.Expr{node.Key, node.Value} {
				if id, ok := variable.(*ast.Ident); ok && id.Obj == object {
					values = append(values, node.X)
				}
			}
		}
		return true
	})
	return values
}

func wu19Parameters(fn *wu19Function) []*ast.Ident {
	var out []*ast.Ident
	if fn.decl.Type.Params != nil {
		for _, field := range fn.decl.Type.Params.List {
			out = append(out, field.Names...)
		}
	}
	return out
}

func wu19ScalarParameter(fn *wu19Function, object *ast.Object) bool {
	if fn.decl.Type.Params != nil {
		for _, field := range fn.decl.Type.Params.List {
			id, ok := field.Type.(*ast.Ident)
			if !ok {
				continue
			}
			switch id.Name {
			case "string", "int", "int32", "int64", "uint", "uint16", "uint32", "bool":
				for _, name := range field.Names {
					if name.Obj == object {
						return true
					}
				}
			}
		}
	}
	return false
}

func wu19EnvironmentParameter(fn *wu19Function, object *ast.Object) bool {
	if fn.decl.Type.Params != nil {
		for _, field := range fn.decl.Type.Params.List {
			array, ok := field.Type.(*ast.ArrayType)
			if !ok {
				continue
			}
			element, ok := array.Elt.(*ast.Ident)
			if !ok || element.Name != "string" {
				continue
			}
			for _, name := range field.Names {
				if name.Obj == object {
					return true
				}
			}
		}
	}
	return false
}

func wu19ParameterIndex(fn *wu19Function, object *ast.Object) int {
	for i, name := range wu19Parameters(fn) {
		if object != nil && name.Obj == object {
			return i
		}
	}
	return -1
}

func (p *wu19Inventory) callOrigin(fn *wu19Function, call *ast.CallExpr, before token.Pos, bindings map[*ast.Object]wu19Reference, seen map[string]bool) wu19Origin {
	if _, conversion := call.Fun.(*ast.ArrayType); conversion && len(call.Args) == 1 {
		return p.origin(fn, call.Args[0], before, bindings, seen)
	}
	id := p.resolvedCallIdentity(fn, call.Fun)
	if p.scalarInterfaceResult(id) {
		return 0 // typed installation/configuration accessor, an explicit site value.
	}
	switch id {
	case "os.Environ":
		p.unresolved["raw os.Environ in "+fn.key] = true
		return wu19Bad
	case "github.com/elicify-ai/omnipus/pkg/tools.scrubbedEnv":
		// Founder dismissal 2026-10-14: accepted God Mode environment risk.
		// This named legacy scrubber is pinned by the real native-path
		// characterization test; it is NOT a generic allowlist root.
		return wu19GodException
	case "github.com/elicify-ai/omnipus/pkg/childenv.Filter":
		return wu19Generic
	case "github.com/elicify-ai/omnipus/pkg/childenv.FilterForRunner":
		return wu19Runner
	}
	if builtin, ok := call.Fun.(*ast.Ident); ok && builtin.Obj == nil {
		switch builtin.Name {
		case "make", "new", "len", "cap":
			return 0 // allocation/capacity is not copied environment data.
		case "string", "int", "int32", "int64", "uint", "uint16", "uint32", "bool":
			if len(call.Args) == 1 {
				return p.origin(fn, call.Args[0], before, bindings, seen)
			}
		case "append":
			var out wu19Origin
			for _, arg := range call.Args {
				out |= p.origin(fn, arg, before, bindings, seen)
			}
			return out &^ wu19MayBeNil // rooted append keeps provenance, not a prior nil-only branch.
		}
	}
	// Primitive wrappers in synthetic fixtures have the same approved identity;
	// real wrappers are present in the loaded model and are followed to childenv.
	if len(p.funcs[id]) == 0 {
		switch id {
		case "github.com/elicify-ai/omnipus/pkg/sandbox.ScrubGatewayEnv":
			return wu19Generic
		case "github.com/elicify-ai/omnipus/pkg/sandbox.ScrubGatewayEnvForRunner":
			return wu19Runner
		}
	}
	if seen["call:"+id] {
		return 0
	}
	seen["call:"+id] = true
	defer delete(seen, "call:"+id)
	if list := p.funcs[id]; len(list) > 0 {
		var out wu19Origin
		for _, callee := range list {
			args := map[*ast.Object]wu19Reference{}
			for i, parameter := range wu19Parameters(callee) {
				if i < len(call.Args) {
					args[parameter.Obj] = wu19Reference{fn, call.Args[i], before, bindings}
				}
			}
			out |= p.returnOrigin(callee, args, seen)
		}
		return out
	}
	// Standard pure formatting/path functions can carry input data, never
	// manufacture parent-environment data. Unknown project helpers are rejected.
	for _, pkg := range []string{"fmt.", "strings.", "strconv.", "path/", "os.Getenv", "os.LookupEnv"} {
		if strings.HasPrefix(id, pkg) {
			var out wu19Origin
			for _, arg := range call.Args {
				out |= p.origin(fn, arg, before, bindings, seen)
			}
			return out
		}
	}
	var printed strings.Builder
	if err := printer.Fprint(&printed, token.NewFileSet(), call.Fun); err != nil {
		panic(err)
	}
	p.unresolved[fmt.Sprintf("%s in %s (%s)", id, fn.key, printed.String())] = true
	return wu19Bad
}

func (p *wu19Inventory) returnOrigin(fn *wu19Function, bindings map[*ast.Object]wu19Reference, seen map[string]bool) wu19Origin {
	var out wu19Origin
	wu19InspectReachable(fn, fn.decl.Body, seen, func(n ast.Node) bool {
		if node, ok := n.(*ast.IfStmt); ok && p.nonNilInputGuard(fn, node, bindings, seen) {
			if node.Else != nil {
				ast.Inspect(node.Else, func(inner ast.Node) bool {
					if ret, ok := inner.(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
						out |= p.origin(fn, ret.Results[0], ret.Pos(), bindings, seen)
					}
					return true
				})
			}
			return false // caller supplied a rooted non-nil input, so nil branch cannot run.
		}
		if ret, ok := n.(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
			if len(ret.Results) > 1 {
				if nilValue, ok := ret.Results[0].(*ast.Ident); ok && nilValue.Name == "nil" {
					if errValue, ok := ret.Results[1].(*ast.Ident); !ok || errValue.Name != "nil" {
						return true // error return, not a usable environment.
					}
				}
			}
			out |= p.origin(fn, ret.Results[0], ret.Pos(), bindings, seen)
		}
		return true
	})
	return out
}

func (p *wu19Inventory) nonNilInputGuard(fn *wu19Function, node *ast.IfStmt, bindings map[*ast.Object]wu19Reference, seen map[string]bool) bool {
	condition, ok := node.Cond.(*ast.BinaryExpr)
	if !ok || condition.Op != token.EQL {
		return false
	}
	left, lok := condition.X.(*ast.Ident)
	right, rok := condition.Y.(*ast.Ident)
	return lok && rok && right.Name == "nil" && p.origin(fn, left, node.Pos(), bindings, seen)&(wu19Generic|wu19Runner) != 0
}
