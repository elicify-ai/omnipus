package sandbox

import (
	"go/ast"
	"go/types"
	"strings"
)

// Resolve method receivers through their declared parameter/receiver/struct
// field types. This is import-resolved AST analysis, not a claim that the entire
// multi-platform repository was go/types checked as one impossible package.
func (p *wu19Inventory) resolvedCallIdentity(fn *wu19Function, expr ast.Expr) string {
	if id := fn.callIdentity(expr); id != "" {
		return id
	}
	s, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if typ := p.declaredType(fn, s.X); typ != nil {
		return p.ownedTypeIdentity(fn, typ) + "." + s.Sel.Name
	}
	return ""
}

func (p *wu19Inventory) declaredType(fn *wu19Function, expr ast.Expr) ast.Expr {
	switch e := expr.(type) {
	case *ast.Ident:
		fields := []*ast.Field{}
		if fn.decl.Recv != nil {
			fields = append(fields, fn.decl.Recv.List...)
		}
		if fn.decl.Type.Params != nil {
			fields = append(fields, fn.decl.Type.Params.List...)
		}
		for _, field := range fields {
			for _, name := range field.Names {
				if fn.file.object(e) != nil && fn.file.object(name) == fn.file.object(e) {
					return field.Type
				}
			}
		}
		if fn.file.object(e) != nil {
			if spec, ok := fn.file.declarations[fn.file.object(e)].(*ast.ValueSpec); ok {
				return spec.Type
			}
			if assignment, ok := fn.file.declarations[fn.file.object(e)].(*ast.AssignStmt); ok && len(assignment.Rhs) == 1 {
				if call, ok := assignment.Rhs[0].(*ast.CallExpr); ok {
					for _, producer := range p.funcs[p.resolvedCallIdentity(fn, call.Fun)] {
						if producer.decl.Type.Results != nil && len(producer.decl.Type.Results.List) > 0 {
							return producer.decl.Type.Results.List[0].Type
						}
					}
				}
			}
		}
	case *ast.SelectorExpr:
		base := p.declaredType(fn, e.X)
		root := p.types[p.ownedTypeIdentity(fn, base)]
		if structure, ok := root.(*ast.StructType); ok {
			for _, field := range structure.Fields.List {
				for _, name := range field.Names {
					if name.Name == e.Sel.Name {
						return field.Type
					}
				}
			}
		}
	}
	return nil
}

func (p *wu19Inventory) ownedTypeIdentity(fn *wu19Function, expr ast.Expr) string {
	if owner := p.typeOwners[expr]; owner != nil {
		context := *fn
		context.file = owner
		return context.typeIdentity(expr)
	}
	return fn.typeIdentity(expr)
}

func (fn *wu19Function) typeIdentity(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return fn.typeIdentity(e.X)
	case *ast.Ident:
		return fn.file.pkg + "." + e.Name
	case *ast.SelectorExpr:
		if imported, ok := e.X.(*ast.Ident); ok {
			if actual := fn.file.imports[imported.Name]; actual != "" {
				return actual + "." + e.Sel.Name
			}
		}
	}
	return ""
}

func (p *wu19Inventory) scalarTupleVariable(fn *wu19Function, object types.Object) bool {
	if object == nil {
		return false
	}
	assignment, ok := fn.file.declarations[object].(*ast.AssignStmt)
	if !ok || len(assignment.Rhs) != 1 {
		return false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	index := -1
	for i, lhs := range assignment.Lhs {
		if id, ok := lhs.(*ast.Ident); ok && fn.file.object(id) == object {
			index = i
		}
	}
	for _, callee := range p.funcs[p.resolvedCallIdentity(fn, call.Fun)] {
		if callee.decl.Type.Results == nil || index < 0 || index >= len(callee.decl.Type.Results.List) {
			return false
		}
		typ, ok := callee.decl.Type.Results.List[index].Type.(*ast.Ident)
		if !ok {
			return false
		}
		switch typ.Name {
		case "string", "int", "int32", "int64", "uint16", "bool":
			return true
		}
	}
	return false
}

func (p *wu19Inventory) scalarInterfaceResult(key string) bool {
	index := strings.LastIndex(key, ".")
	if index < 0 {
		return false
	}
	contract, ok := p.types[key[:index]].(*ast.InterfaceType)
	if !ok {
		return false
	}
	for _, method := range contract.Methods.List {
		if len(method.Names) != 1 || method.Names[0].Name != key[index+1:] {
			continue
		}
		fn, ok := method.Type.(*ast.FuncType)
		if !ok || fn.Results == nil || len(fn.Results.List) != 1 {
			return false
		}
		result, ok := fn.Results.List[0].Type.(*ast.Ident)
		return ok && result.Name == "string"
	}
	return false
}
