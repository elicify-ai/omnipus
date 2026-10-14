package sandbox

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path"
	"strconv"
	"strings"
)

type wu19File struct {
	path, pkg      string
	ast            *ast.File
	imports        map[string]string
	identities     *types.Info
	declarations   map[types.Object]ast.Node
	identityErrors []error
}

type wu19Function struct {
	file *wu19File
	decl *ast.FuncDecl
	key  string
}

type wu19Finding struct{ path, symbol, reason string }

type wu19Inventory struct {
	files         []*wu19File
	funcs         map[string][]*wu19Function
	unresolved    map[string]bool
	types         map[string]ast.Expr
	typeOwners    map[ast.Expr]*wu19File
	globals       map[string]wu19Reference
	globalObjects map[types.Object]bool
}

func wu19NewInventory() *wu19Inventory {
	return &wu19Inventory{funcs: map[string][]*wu19Function{}, unresolved: map[string]bool{}, types: map[string]ast.Expr{}, typeOwners: map[ast.Expr]*wu19File{}, globals: map[string]wu19Reference{}, globalObjects: map[types.Object]bool{}}
}

func (p *wu19Inventory) addFile(name, pkg string, file *ast.File, fset *token.FileSet) {
	s := &wu19File{path: name, pkg: pkg, ast: file, imports: map[string]string{}}
	s.resolveObjects(fset)
	for _, imp := range file.Imports {
		actual, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			panic(err) // parser accepted the file; an invalid import identity is fatal.
		}
		alias := path.Base(actual)
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		s.imports[alias] = actual
	}
	p.files = append(p.files, s)
	ast.Inspect(file, func(n ast.Node) bool {
		if field, ok := n.(*ast.Field); ok {
			p.typeOwners[field.Type] = s
		}
		return true
	})
	for _, decl := range file.Decls {
		if group, ok := decl.(*ast.GenDecl); ok {
			for _, spec := range group.Specs {
				if value, ok := spec.(*ast.ValueSpec); ok {
					for i, name := range value.Names {
						if i < len(value.Values) {
							context := &wu19Function{file: s, key: pkg + "." + name.Name, decl: &ast.FuncDecl{Type: &ast.FuncType{}, Body: &ast.BlockStmt{}}}
							p.globals[pkg+"."+name.Name] = wu19Reference{context, value.Values[i], value.End(), nil}
							p.globalObjects[s.object(name)] = true
						}
					}
				}
				if typ, ok := spec.(*ast.TypeSpec); ok {
					p.types[pkg+"."+typ.Name.Name] = typ.Type
					p.typeOwners[typ.Type] = s
				}
			}
		}
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			key := pkg + "." + fn.Name.Name
			if fn.Recv != nil {
				key = pkg + "." + wu19TypeName(fn.Recv.List[0].Type) + "." + fn.Name.Name
			}
			p.funcs[key] = append(p.funcs[key], &wu19Function{file: s, decl: fn, key: key})
		}
	}
}

func wu19TypeName(e ast.Expr) string {
	switch n := e.(type) {
	case *ast.StarExpr:
		return wu19TypeName(n.X)
	case *ast.Ident:
		return n.Name
	case *ast.SelectorExpr:
		return n.Sel.Name
	}
	return ""
}

func (f *wu19Function) callIdentity(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		if f.file.object(id) == nil && f.file.imports["."] == "os/exec" && (id.Name == "Command" || id.Name == "CommandContext") {
			return "os/exec." + id.Name
		}
		if f.file.object(id) != nil {
			if alias, ok := f.file.declarations[f.file.object(id)].(*ast.AssignStmt); ok && len(alias.Rhs) == 1 {
				if selector, ok := alias.Rhs[0].(*ast.SelectorExpr); ok {
					return f.callIdentity(selector)
				}
			}
		}
		return f.file.pkg + "." + id.Name
	}
	s, ok := e.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	id, ok := s.X.(*ast.Ident)
	if !ok {
		return ""
	}
	if imported := f.file.imports[id.Name]; imported != "" && f.file.object(id) == nil {
		return imported + "." + s.Sel.Name
	}
	if f.decl.Recv != nil {
		for _, receiver := range f.decl.Recv.List {
			for _, name := range receiver.Names {
				if f.file.object(name) == f.file.object(id) {
					return f.typeIdentity(receiver.Type) + "." + s.Sel.Name
				}
			}
		}
	}
	if f.decl.Type.Params != nil {
		for _, field := range f.decl.Type.Params.List {
			for _, name := range field.Names {
				if f.file.object(name) == f.file.object(id) {
					return f.typeIdentity(field.Type) + "." + s.Sel.Name
				}
			}
		}
	}
	return ""
}

func (p *wu19Inventory) scan() (int, []wu19Finding) {
	var count int
	var findings []wu19Finding
	for _, list := range p.funcs {
		for _, fn := range list {
			ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				id := fn.callIdentity(call.Fun)
				if id != "os/exec.Command" && id != "os/exec.CommandContext" {
					return true
				}
				count++
				if reason := p.checkConstructor(fn, call); reason != "" {
					findings = append(findings, wu19Finding{fn.file.path, fn.key, reason})
				}
				return true
			})
		}
	}
	return count, findings
}

func wu19AssignedObject(fn *wu19Function, constructor *ast.CallExpr) types.Object {
	body := fn.decl.Body
	var object types.Object
	ast.Inspect(body, func(n ast.Node) bool {
		if a, ok := n.(*ast.AssignStmt); ok {
			for i, rhs := range a.Rhs {
				if rhs == constructor && i < len(a.Lhs) {
					if id, ok := a.Lhs[i].(*ast.Ident); ok {
						object = fn.file.object(id)
					}
				}
			}
		}
		return true
	})
	return object
}

func wu19SameCommand(fn *wu19Function, e ast.Expr, object types.Object, body *ast.BlockStmt, seen map[types.Object]bool) bool {
	id, ok := e.(*ast.Ident)
	if !ok || fn.file.object(id) == nil || seen[fn.file.object(id)] {
		return false
	}
	if fn.file.object(id) == object {
		return true
	}
	seen[fn.file.object(id)] = true
	var source ast.Expr
	ast.Inspect(body, func(n ast.Node) bool {
		if a, ok := n.(*ast.AssignStmt); ok && len(a.Lhs) == len(a.Rhs) {
			for i, lhs := range a.Lhs {
				if v, ok := lhs.(*ast.Ident); ok && fn.file.object(v) == fn.file.object(id) {
					source = a.Rhs[i]
				}
			}
		}
		return true
	})
	return source != nil && wu19SameCommand(fn, source, object, body, seen)
}

func wu19EnclosingBlock(body *ast.BlockStmt, node ast.Node) *ast.BlockStmt {
	block := body
	ast.Inspect(body, func(n ast.Node) bool {
		if b, ok := n.(*ast.BlockStmt); ok && b.Pos() <= node.Pos() && node.End() <= b.End() && b.End()-b.Pos() < block.End()-block.Pos() {
			block = b
		}
		return true
	})
	return block
}

func (p *wu19Inventory) checkConstructor(fn *wu19Function, call *ast.CallExpr) string {
	cmd := wu19AssignedObject(fn, call)
	if cmd == nil {
		return "exec constructor has no verifiable command identity/environment assignment"
	}
	var assignments []*ast.AssignStmt
	var environments []ast.Expr
	boundary := fn.decl.Body.End()
	ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
		if a, ok := n.(*ast.AssignStmt); ok && len(a.Lhs) == len(a.Rhs) {
			for i, lhs := range a.Lhs {
				if s, ok := lhs.(*ast.SelectorExpr); ok && s.Sel.Name == "Env" && wu19SameCommand(fn, s.X, cmd, fn.decl.Body, map[types.Object]bool{}) {
					assignments = append(assignments, a)
					environments = append(environments, a.Rhs[i])
				}
			}
		}
		if ret, ok := n.(*ast.ReturnStmt); ok {
			for _, e := range ret.Results {
				if wu19SameCommand(fn, e, cmd, fn.decl.Body, map[types.Object]bool{}) && ret.Pos() < boundary {
					boundary = ret.Pos()
				}
			}
		}
		if c, ok := n.(*ast.CallExpr); ok {
			if s, ok := c.Fun.(*ast.SelectorExpr); ok && wu19SameCommand(fn, s.X, cmd, fn.decl.Body, map[types.Object]bool{}) {
				switch s.Sel.Name {
				case "Start", "Run", "Output", "CombinedOutput":
					if c.Pos() < boundary {
						boundary = c.Pos()
					}
				}
			}
			// Returning/passing a command into another subsystem is also a
			// boundary. Unknown helpers cannot be assumed not to start it.
			for _, arg := range c.Args {
				if wu19SameCommand(fn, arg, cmd, fn.decl.Body, map[types.Object]bool{}) && c.Pos() < boundary && !p.nonStartingCommandHelper(fn.callIdentity(c.Fun), map[string]bool{}) {
					boundary = c.Pos()
				}
			}
		}
		return true
	})
	if len(assignments) != 1 {
		return fmt.Sprintf("command Env assigned %d times; require exactly one unconditional assignment before launch/escape", len(assignments))
	}
	a := assignments[0]
	if a.Pos() <= call.Pos() || a.Pos() >= boundary || wu19EnclosingBlock(fn.decl.Body, a) != wu19EnclosingBlock(fn.decl.Body, call) {
		return "command Env assignment does not unconditionally precede launch/escape"
	}
	const tools = "github.com/elicify-ai/omnipus/pkg/tools."
	// Founder dismissal 2026-10-14, accepted risk: these EXACT three sites
	// may use the documented God scrub, never an arbitrary raw helper.
	godSite := fn.key == tools+"ExecTool.runUnconstrained" || fn.key == tools+"ExecTool.runBackground" || fn.key == tools+"EnvironmentSetupTool.startSession"
	origin := wu19Origin(0)
	modes := []bool{false}
	if godSite {
		modes = []bool{false, true}
	}
	if fn.key == tools+"ExecTool.runUnconstrained" {
		modes = []bool{true} // this function is the God-only route, not a non-God spawn site.
	}
	for _, god := range modes {
		facts := map[string]bool{"god-mode-known": true, "god-mode-value": god}
		if fn.key == tools+"ExecTool.runBackground" {
			facts["run-background-known"], facts["run-background-value"] = true, true
		}
		if fn.key == tools+"ExecTool.runUnconstrained" {
			facts["run-background-known"], facts["run-background-value"] = true, false
		}
		value := p.origin(fn, environments[0], a.Pos(), nil, facts)
		approved := value & (wu19Generic | wu19Runner)
		if godSite && god {
			approved |= value & wu19GodException
		}
		if value&(wu19Bad|wu19MayBeNil) != 0 || approved == 0 || (value&wu19GodException != 0 && (!godSite || !god)) {
			return fmt.Sprintf("command environment has raw/unresolved provenance or no approved scrub root (God=%t origin=%d)", god, value)
		}
		origin |= value
	}
	externalWorker := fn.file.pkg == "github.com/elicify-ai/omnipus/pkg/agent/runner" && (strings.HasSuffix(fn.key, ".Run") || strings.HasSuffix(fn.key, ".detectCLIVersion") || strings.HasSuffix(fn.key, ".probeCLIConnection"))
	if externalWorker && origin&wu19Runner == 0 {
		return "external runner environment must originate from ScrubGatewayEnvForRunner"
	}
	unsafeHandoff := false
	ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && c.Pos() > a.Pos() {
			for _, arg := range c.Args {
				if wu19SameCommand(fn, arg, cmd, fn.decl.Body, map[types.Object]bool{}) && !p.commandMutationSafe(fn, c, cmd, map[string]bool{}) {
					unsafeHandoff = true
					p.unresolved["unsafe command transfer: "+p.resolvedCallIdentity(fn, c.Fun)+" from "+fn.key] = true
				}
			}
		}
		return true
	})
	if unsafeHandoff {
		return "command-pointer helper may reopen raw/nil/unresolved environment after initial scrub"
	}
	return ""
}
