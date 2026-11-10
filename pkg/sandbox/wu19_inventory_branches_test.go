package sandbox

import (
	"go/ast"
	"go/token"
)

// Three-value evaluation of the one documented mode fact. Unknown conditions
// keep both paths reachable. It never treats an arbitrary flag as a safe branch.
func wu19ModeTruth(fn *wu19Function, expr ast.Expr, facts map[string]bool) int {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return wu19ModeTruth(fn, e.X, facts)
	case *ast.Ident:
		if e.Name == "runInBackground" && facts["run-background-known"] && fn.key == "github.com/elicify-ai/omnipus/pkg/tools.ExecTool.executeRun" {
			if facts["run-background-value"] {
				return 1
			}
			return 0
		}
	case *ast.SelectorExpr:
		if e.Sel.Name == "godMode" && facts["god-mode-known"] && wu19OwnModeReceiver(fn, e.X) {
			if facts["god-mode-value"] {
				return 1
			}
			return 0
		}
	case *ast.UnaryExpr:
		if e.Op == token.NOT {
			v := wu19ModeTruth(fn, e.X, facts)
			if v >= 0 {
				return 1 - v
			}
		}
	case *ast.BinaryExpr:
		left, right := wu19ModeTruth(fn, e.X, facts), wu19ModeTruth(fn, e.Y, facts)
		switch e.Op {
		case token.LAND:
			if left == 0 || right == 0 {
				return 0
			}
			if left == 1 && right == 1 {
				return 1
			}
		case token.LOR:
			if left == 1 || right == 1 {
				return 1
			}
			if left == 0 && right == 0 {
				return 0
			}
		}
	}
	return -1
}

func wu19OwnModeReceiver(fn *wu19Function, expr ast.Expr) bool {
	if fn.file.pkg != "github.com/elicify-ai/omnipus/pkg/tools" || fn.decl.Recv == nil {
		return false
	}
	typ := wu19TypeName(fn.decl.Recv.List[0].Type)
	if typ != "ExecTool" && typ != "EnvironmentSetupTool" {
		return false
	}
	id, ok := expr.(*ast.Ident)
	if !ok || fn.file.object(id) == nil {
		return false
	}
	for _, field := range fn.decl.Recv.List {
		for _, name := range field.Names {
			if fn.file.object(id) == fn.file.object(name) {
				return true
			}
		}
	}
	return false
}

func wu19InspectReachable(fn *wu19Function, root ast.Node, facts map[string]bool, visit func(ast.Node) bool) {
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil || !visit(n) {
			return false
		}
		switch node := n.(type) {
		case *ast.IfStmt:
			if truth := wu19ModeTruth(fn, node.Cond, facts); truth >= 0 {
				if node.Init != nil {
					wu19InspectReachable(fn, node.Init, facts, visit)
				}
				if truth == 1 {
					wu19InspectReachable(fn, node.Body, facts, visit)
				} else if node.Else != nil {
					wu19InspectReachable(fn, node.Else, facts, visit)
				}
				return false
			}
		case *ast.SwitchStmt:
			if node.Tag == nil {
				if node.Init != nil {
					wu19InspectReachable(fn, node.Init, facts, visit)
				}
				for _, statement := range node.Body.List {
					branch, ok := statement.(*ast.CaseClause)
					if !ok {
						panic("inventory switch contains a non-case statement")
					}
					truth := -1
					if len(branch.List) == 1 {
						truth = wu19ModeTruth(fn, branch.List[0], facts)
					}
					if truth != 0 {
						for _, item := range branch.Body {
							wu19InspectReachable(fn, item, facts, visit)
						}
					}
					if truth == 1 {
						break // later/default cases cannot run after a guaranteed match.
					}
				}
				return false
			}
		}
		return true
	})
}
