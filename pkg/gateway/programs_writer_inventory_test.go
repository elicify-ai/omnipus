package gateway

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
)

type pgWriterReference struct {
	caller, target, file string
	offset               int
}

func (r pgWriterReference) key() string { return r.caller + " -> " + r.target }
func (r pgWriterReference) identity() string {
	return fmt.Sprintf("%s:%d:%s", r.file, r.offset, r.key())
}

func pgWriterRootObjects(source *pgWriterSource) (map[string]*types.Func, error) {
	pkg, err := source.importer.Import(pgWriterModule + "/pkg/credentials")
	if err != nil {
		return nil, err
	}
	obj := pkg.Scope().Lookup("Store")
	if obj == nil {
		return nil, fmt.Errorf("BLOCKED: real credentials.Store missing — I3/S113")
	}
	set := types.NewMethodSet(types.NewPointer(obj.Type()))
	roots := map[string]*types.Func{}
	for _, name := range []string{"CommitProgramCredential", "RemoveProgramCredential"} {
		selection := set.Lookup(pkg, name)
		if selection == nil {
			return nil, fmt.Errorf("BLOCKED: Store.%s missing — I3/S113", name)
		}
		fn, ok := selection.Obj().(*types.Func)
		if !ok {
			return nil, fmt.Errorf("Store.%s is not a type-resolved method", name)
		}
		roots[pgWriterSymbol(fn)] = fn
	}
	return roots, nil
}

func pgWriterFunctionRefs(pkg *pgWriterPackage, visit func(string, *ast.Ident, *types.Func)) {
	for _, file := range pkg.files {
		for _, declaration := range file.Decls {
			owner := ""
			if fn, ok := declaration.(*ast.FuncDecl); ok {
				object, ok := pkg.info.Defs[fn.Name].(*types.Func)
				if !ok {
					continue // no executable function body; checker owns validity
				}
				owner = pgWriterSymbol(object)
			}
			ast.Inspect(declaration, func(node ast.Node) bool {
				id, ok := node.(*ast.Ident)
				if !ok {
					return true
				}
				fn, ok := pkg.info.Uses[id].(*types.Func)
				if !ok {
					return true
				}
				caller := owner
				if caller == "" {
					caller = file.Name.Name + ".<package-init>"
				}
				visit(caller, id, fn.Origin())
				return true
			})
		}
	}
}

// Include all Programs bridges reaching a root by calls OR references. This
// includes CommitCardToken, not only Service methods, so its callback fallback
// cannot evade writer accounting. This is a finite source-reference closure,
// not a claim of arbitrary route/callback reachability.
func pgWriterProgramTargets(pkg *pgWriterPackage, roots map[string]*types.Func) map[string]*types.Func {
	targets := map[string]*types.Func{}
	for key, fn := range roots {
		targets[key] = fn
	}
	owners := map[string]*types.Func{}
	for _, file := range pkg.files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				if obj, ok := pkg.info.Defs[fn.Name].(*types.Func); ok {
					owners[pgWriterSymbol(obj)] = obj
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		pgWriterFunctionRefs(pkg, func(owner string, _ *ast.Ident, ref *types.Func) {
			if targets[pgWriterSymbol(ref)] != nil && targets[owner] == nil && owners[owner] != nil {
				targets[owner] = owners[owner]
				changed = true
			}
		})
	}
	return targets
}

func pgWriterReferences(set *token.FileSet, pkg *pgWriterPackage, targets map[string]*types.Func) []pgWriterReference {
	var refs []pgWriterReference
	pgWriterFunctionRefs(pkg, func(owner string, id *ast.Ident, fn *types.Func) {
		matched := map[string]bool{}
		if targets[pgWriterSymbol(fn)] != nil {
			matched[pgWriterSymbol(fn)] = true
		}
		// Calls through a structural interface are still references to a
		// capability implemented by the protected receiver. Do not lose them
		// merely because go/types resolves the interface's method object.
		sig, ok := fn.Type().(*types.Signature)
		if ok && sig.Recv() != nil {
			if iface, ok := sig.Recv().Type().Underlying().(*types.Interface); ok {
				for key, target := range targets {
					targetSig, ok := target.Type().(*types.Signature)
					if ok && target.Name() == fn.Name() && targetSig.Recv() != nil && types.Implements(targetSig.Recv().Type(), iface) {
						matched[key] = true
					}
				}
			}
		}
		pos := set.Position(id.Pos())
		for key := range matched {
			refs = append(refs, pgWriterReference{caller: owner, target: key, file: pos.Filename, offset: pos.Offset})
		}
	})
	sort.Slice(refs, func(i, j int) bool { return refs[i].identity() < refs[j].identity() })
	return refs
}

func pgWriterInventoryFailures(refs []pgWriterReference, allowed map[string]int) []string {
	counts := map[string]int{}
	for _, ref := range refs {
		counts[ref.key()]++
	}
	var failures []string
	for key, count := range counts {
		if count != allowed[key] {
			failures = append(failures, fmt.Sprintf("%s: references=%d allowed=%d", key, count, allowed[key]))
		}
	}
	for key, count := range allowed {
		if counts[key] == 0 && count != 0 {
			failures = append(failures, key+": required reference absent")
		}
	}
	sort.Strings(failures)
	return failures
}

func pgWriterGatedAllowlist() map[string]int {
	// Oracle: I3/FR043/048/051 and ruling H46. Exactly these person Programs
	// entry functions or validated pending-card path may reference writers.
	// Counts are one reference per listed edge, not discovered from a run.
	local := map[string]string{
		"programs.CommitCardToken":                   "credentials.Store.CommitProgramCredential",
		"programs.Service.Publish":                   "credentials.Store.CommitProgramCredential",
		"programs.Service.Disconnect":                "credentials.Store.RemoveProgramCredential",
		"gateway.restAPI.publishProgramCard":         "programs.Service.Publish",
		"gateway.restAPI.saveProgramCredential":      "programs.Service.Publish",
		"gateway.restAPI.deleteProgram":              "programs.Service.Disconnect",
		"gateway.restAPI.disconnectProgram":          "programs.Service.Disconnect",
		"secretrequest.Registry.submitProgramLocked": "programs.CommitCardToken",
	}
	out := map[string]int{}
	for owner, target := range local {
		out[pgWriterModule+"/pkg/"+owner+" -> "+pgWriterModule+"/pkg/"+target] = 1
	}
	return out
}
