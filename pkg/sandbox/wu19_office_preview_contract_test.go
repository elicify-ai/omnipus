package sandbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

const wu19PreviewPackage = "github.com/elicify-ai/omnipus/pkg/preview"
const wu19PreviewLauncher = wu19PreviewPackage + "/launcher"

// Q62 A / AD-7 / FR-009 / MV-2. Declarations/imports are pinned to the
// security-reviewed source; comments are not an environment-safety proof.
// Runtime final-target goldens independently check the specification's values.
func (p *wu19Inventory) officePreviewClosedEnvironment() string {
	raw, err := os.ReadFile(filepath.Join("..", "..", "tests", "officepreview", "closed-env-source-pins.json"))
	if err != nil {
		return "closed preview contract drift: cannot read reviewed pins: " + err.Error()
	}
	var pins struct {
		Reviewed string            `json:"reviewed_source"`
		Files    map[string]string `json:"files"`
	}
	if err = json.Unmarshal(raw, &pins); err != nil || len(pins.Files) != 21 || pins.Reviewed != "59855878df8af4bace58de6a48604a9041f35010" {
		return "closed preview contract drift: invalid reviewed pin manifest"
	}
	seen := map[string]bool{}
	for _, file := range p.files {
		name := wu19OfficeFileName(file)
		want, pinned := pins.Files[name]
		if file.pkg == wu19PreviewLauncher && !pinned {
			return "closed preview contract drift: unreviewed launcher file " + name
		}
		if !pinned {
			continue
		}
		if seen[name] {
			return "closed preview contract drift: duplicate source identity " + name
		}
		seen[name] = true
		var normalized bytes.Buffer
		if err = format.Node(&normalized, token.NewFileSet(), file.ast); err != nil {
			return "closed preview contract drift: cannot normalize " + name
		}
		digest := sha256.Sum256(normalized.Bytes())
		if hex.EncodeToString(digest[:]) != want {
			return "closed preview contract drift: reviewed declarations changed in " + name
		}
	}
	if len(seen) != len(pins.Files) {
		return "closed preview contract drift: a reviewed source file is missing"
	}
	if reason := p.officePreviewBoundaryReferences(); reason != "" {
		return reason
	}
	return p.officePreviewStartupEnvironment()
}

// Only these TWO constructor sites can use the closed-list exception. Unix
// Exec is pinned by the same proof, not a general os.Environ permission.
func (p *wu19Inventory) officePreviewConstructor(fn *wu19Function) (bool, string) {
	name := wu19OfficeFileName(fn.file)
	candidate := fn.key == wu19PreviewLauncher+".Command" && name == "pkg/preview/launcher/launcher.go" ||
		fn.key == wu19PreviewLauncher+".Main" && name == "pkg/preview/launcher/child_windows.go"
	if !candidate {
		return false, ""
	}
	return true, p.officePreviewClosedEnvironment()
}

func (p *wu19Inventory) officePreviewRelevant() bool {
	for _, file := range p.files {
		if file.pkg == wu19PreviewPackage || file.pkg == wu19PreviewLauncher {
			return true
		}
	}
	return false
}

func wu19OfficeDrift(format string, args ...any) string {
	return "closed preview contract drift: " + fmt.Sprintf(format, args...)
}

func wu19OfficeContext(file *wu19File, decl ast.Decl) *wu19Function {
	fn := &wu19Function{file: file, key: file.pkg + ".<package-init>", decl: &ast.FuncDecl{Type: &ast.FuncType{}, Body: &ast.BlockStmt{}}}
	if function, ok := decl.(*ast.FuncDecl); ok {
		fn.decl = function
		fn.key = file.pkg + "." + function.Name.Name
		if function.Recv != nil {
			fn.key = file.pkg + "." + wu19TypeName(function.Recv.List[0].Type) + "." + function.Name.Name
		}
	}
	return fn
}

// No new producer, alias, forwarding reference or alternate entry can inherit
// the approval. References as function values are checked as well as calls.
func (p *wu19Inventory) officePreviewBoundaryReferences() string {
	allowed := map[string]map[string]int{
		wu19PreviewLauncher + ".Command":                   {wu19PreviewLauncher + ".Start": 1},
		wu19PreviewLauncher + ".Start":                     {wu19PreviewPackage + ".protocolProgram.run": 1, wu19PreviewPackage + ".officeConversion.startOnce": 1},
		wu19PreviewLauncher + ".Main":                      {"github.com/elicify-ai/omnipus/pkg/app.runHiddenMode": 1},
		wu19PreviewPackage + ".officeConversion.startOnce": {wu19PreviewPackage + ".officeConversion.run": 1},
	}
	for _, file := range p.files {
		if file.imports["."] == wu19PreviewLauncher {
			return wu19OfficeDrift("unreviewed dot-import boundary in %s", file.path)
		}
		for _, decl := range file.ast.Decls {
			fn := wu19OfficeContext(file, decl)
			called := map[ast.Expr]bool{}
			ast.Inspect(decl, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					called[call.Fun] = true
				}
				return true
			})
			var failure string
			ast.Inspect(decl, func(n ast.Node) bool {
				var expr ast.Expr
				switch node := n.(type) {
				case *ast.CallExpr:
					expr = node.Fun
				case *ast.SelectorExpr:
					if !called[node] {
						expr = node
					}
				}
				if expr == nil {
					return true
				}
				id := p.resolvedCallIdentity(fn, expr)
				if owners, boundary := allowed[id]; boundary {
					if owners[fn.key] != 1 {
						failure = wu19OfficeDrift("unreviewed boundary reference %s from %s", id, fn.key)
					} else {
						owners[fn.key]--
					}
				}
				return true
			})
			if failure != "" {
				return failure
			}
		}
	}
	for boundary, owners := range allowed {
		for owner, remaining := range owners {
			if remaining != 0 {
				return wu19OfficeDrift("missing boundary reference %s from %s", boundary, owner)
			}
		}
	}
	return ""
}

// Package initialization runs before hidden-mode dispatch. Reject project
// initialization/global-initializer call chains that enrich that process's
// environment; a helper does not hide a Setenv/Clearenv/Unsetenv call.
func (p *wu19Inventory) officePreviewStartupEnvironment() string {
	var inspect func(*wu19Function, ast.Node, map[string]bool) bool
	inspect = func(fn *wu19Function, node ast.Node, seen map[string]bool) bool {
		unsafe := false
		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id := p.resolvedCallIdentity(fn, call.Fun)
			for _, prefix := range []string{"os.", "syscall.", "golang.org/x/sys/unix.", "golang.org/x/sys/windows."} {
				if strings.HasPrefix(id, prefix) {
					name := strings.TrimPrefix(id, prefix)
					if name == "Setenv" || name == "Clearenv" || name == "Unsetenv" || name == "SetEnvironmentVariable" {
						unsafe = true
					}
				}
			}
			if !seen[id] {
				seen[id] = true
				for _, callee := range p.funcs[id] {
					unsafe = inspect(callee, callee.decl.Body, seen) || unsafe
				}
			}
			return true
		})
		return unsafe
	}
	for _, file := range p.files {
		for _, decl := range file.ast.Decls {
			fn := wu19OfficeContext(file, decl)
			if function, ok := decl.(*ast.FuncDecl); ok && function.Name.Name == "init" && inspect(fn, function.Body, map[string]bool{}) {
				return wu19OfficeDrift("initialization enriches environment in %s", file.path)
			}
			if group, ok := decl.(*ast.GenDecl); ok && group.Tok == token.VAR && inspect(fn, group, map[string]bool{}) {
				return wu19OfficeDrift("global initializer enriches environment in %s", file.path)
			}
		}
	}
	return ""
}
