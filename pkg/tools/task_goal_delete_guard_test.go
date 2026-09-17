// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_goal_delete_guard_test.go is the deletion-side sibling of
// task_goal_terminal_guard_test.go, and it exists for the same reason that one
// does: the rule it protects was previously enforced by a sentence.
//
// GOAL-FR-044 says a goal MUST NOT outlive its owner. task.Store.Delete cannot
// enforce it — pkg/goal imports pkg/task, so the dependency only runs one way,
// and that store's own doc comment says as much. The rule therefore lives in
// the CALLERS, and until this consolidation it lived there as three
// hand-mirrored private copies (pkg/gateway, pkg/tools, pkg/sysagent/tools),
// each with its own error handling. Three copies of a rule is how a fourth
// delete surface ships without it, silently, and how the same three copies
// ended up giving three different answers when the cleanup failed.
//
// There is now ONE implementation — tools.RemoveTaskGoalRecords — and THIS
// test is what keeps its call-site set closed. It parses every non-test file
// under pkg/ and finds every function that consumes a two-value `.Delete(...)`
// result — the shape task.Store.Delete's `(unblockedIDs, err)` signature
// forces on every one of its callers. That set must match the registry below
// EXACTLY, so a NEW task-delete surface fails this test until its author
// either calls the cleanup or writes down why the call is not a task delete.
//
// The scan deliberately does NOT filter on the receiver's name. A detector
// keyed on `taskStore`/`store` would be silently defeated by the next author
// who spells the variable `ts`, and a guard that silently stops guarding is
// worse than no guard (the sibling guard's own words). The cost is that a
// handful of unrelated two-value Delete calls have to be classified once; that
// is the same cost the sibling guard pays for its non-terminal status writers,
// and it is cheap compared to the defect it prevents.
//
// Shared machinery (funcKey, exprName, sortedKeys) comes from
// task_goal_terminal_guard_test.go — same package, so the scanner is extended
// rather than copied. Copying it would have been this file's own subject
// matter.
package tools

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goalDeleteCleanupNames are the call expressions that count as routing
// through the shared task-delete -> goal-removal rule. Two spellings because
// the function is reached bare from inside pkg/tools and qualified from
// everywhere else.
var goalDeleteCleanupNames = map[string]bool{
	"tools.RemoveTaskGoalRecords": true,
	"RemoveTaskGoalRecords":       true,
}

// taskDeleter is one row of the registry: a function that consumes a two-value
// Delete result, and the decision made about it.
type taskDeleter struct {
	// where is "<pkg dir>.<function>", the key the scan produces.
	where string
	// mustClean is true when the Delete being consumed is task.Store.Delete —
	// i.e. this function removes a TASK, and must therefore remove that task's
	// paired goal record first (GOAL-FR-044/EC-4).
	mustClean bool
	// why records the reason a row is exempt. Required when mustClean is
	// false — an exemption with no stated reason is how the mirrored-copy
	// defect was built in the first place.
	why string
}

// knownTaskDeleters is the closed registry. Every function under pkg/ that
// consumes a two-value Delete result must appear here exactly once.
var knownTaskDeleters = []taskDeleter{
	// ---- the task-delete surfaces: each must remove the paired goal first --
	{where: "pkg/gateway.restAPI.handleTaskDelete", mustClean: true},
	{where: "pkg/tools.TaskDeleteTool.Execute", mustClean: true},
	{where: "pkg/sysagent/tools.TaskDeleteTool.Execute", mustClean: true},

	// ---- not task deletes: each states what it actually deletes -----------
	{
		where: "pkg/gateway.restAPI.handleWorkspaceMediaDelete",
		why: "deletes a media-library entry (library.Library.Delete returns the removed " +
			"entry alongside its error, which is why it lands in this scan). A media " +
			"entry owns no goal record.",
	},
	{
		where: "pkg/gateway.restAPIHandleUpload.finishWorkspaceUpload",
		why: "rolls back already-staged media-library entries when a later file in the " +
			"same multi-file upload fails. Media entries, not tasks.",
	},
	{
		where: "pkg/gateway.restAPI.cleanupWorkspaceUploads",
		why:   "removes media-library entries staged by a workspace upload. Media entries, not tasks.",
	},
	{
		where: "pkg/channels/feishu.FeishuChannel.ReactToMessage",
		why: "removes a Feishu message REACTION over the Lark SDK " +
			"(MessageReaction.Delete returns (resp, err)). Nothing to do with tasks.",
	},
}

// TestEveryTaskDeleterRemovesItsGoalRecord is the guard.
//
// The oracle is a SET EQUALITY for the same reason the sibling guard's is: a
// new delete surface nobody classified must fail as loudly as a classified one
// that lost its cleanup call.
func TestEveryTaskDeleterRemovesItsGoalRecord(t *testing.T) {
	found := scanTwoValueDeleters(t)

	known := make(map[string]taskDeleter, len(knownTaskDeleters))
	for _, d := range knownTaskDeleters {
		if _, dup := known[d.where]; dup {
			t.Fatalf("registry is malformed: %q is listed twice", d.where)
		}
		if !d.mustClean && strings.TrimSpace(d.why) == "" {
			t.Errorf("registry entry %q claims exemption from the goal cleanup with no stated reason.\n"+
				"An undocumented exemption is how the three-mirrored-copies defect was built.", d.where)
		}
		known[d.where] = d
	}

	for where := range found {
		if _, ok := known[where]; !ok {
			t.Errorf("UNCLASSIFIED two-value Delete consumer: %s\n"+
				"task.Store.Delete returns (unblockedIDs, err), so every task-delete surface has "+
				"this shape. A deleted task MUST first have its paired goal record transitioned "+
				"and removed (GOAL-FR-044/EC-4) — otherwise the record survives as an `active` "+
				"goal owned by a task id that no longer resolves, and only the retention sweep "+
				"would ever touch it again.\n"+
				"Add a row to knownTaskDeleters in this file: either mustClean (and call "+
				"tools.RemoveTaskGoalRecords BEFORE removing the task), or an exemption with a "+
				"written reason saying what this function actually deletes.", where)
		}
	}
	for where, d := range known {
		cleaned, ok := found[where]
		if !ok {
			t.Errorf("registry entry %q no longer consumes a two-value Delete result.\n"+
				"Either it was renamed/removed (delete the row) or the scanner stopped seeing it "+
				"(fix the scanner — a guard that silently stops guarding is worse than no guard).\n"+
				"What the scanner DID find: %v", where, sortedKeys(found))
			continue
		}
		switch {
		case d.mustClean && !cleaned:
			t.Errorf("TASK-DELETE SURFACE WITH NO GOAL CLEANUP: %s\n"+
				"This function deletes a task but never calls RemoveTaskGoalRecords. The task's "+
				"paired goal record then outlives it as an unreferenced orphan — permanently "+
				"`active` if the task had ever run — which is exactly what GOAL-FR-044 forbids.\n"+
				"Call tools.RemoveTaskGoalRecords BEFORE task.Store.Delete, and treat a failure "+
				"as fatal to the delete: afterwards the task is already gone and the orphan can "+
				"only be reported, not prevented.", where)
		case !d.mustClean && cleaned:
			t.Errorf("registry entry %q is marked exempt (%q) but DOES call the goal cleanup.\n"+
				"One of the two is wrong. If it really deletes tasks, flip the row to mustClean; "+
				"if the call is spurious, remove it.", where, d.why)
		}
	}
}

// scanTwoValueDeleters parses every non-test Go file under pkg/ and returns the
// functions that consume a two-value `X.Delete(...)` result, mapped to whether
// that same function also calls the shared goal cleanup.
//
// Two-value consumption is the marker because task.Store.Delete's signature —
// `(unblockedIDs []string, err error)` — forces it on every caller, including
// the ones that discard both results. A single-value `.Delete(id)` (goal.Store,
// plan.Store, credential stores, sync.Map) is a different method entirely and
// is not in scope.
func scanTwoValueDeleters(t *testing.T) map[string]bool {
	t.Helper()
	root, err := filepath.Abs("..") // pkg/
	if err != nil {
		t.Fatalf("resolve pkg/ root: %v", err)
	}
	out := map[string]bool{}
	fset := token.NewFileSet()

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if base == "generated" || base == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, rErr := filepath.Rel(filepath.Dir(root), path)
		if rErr != nil {
			return rErr
		}
		pkgDir := filepath.ToSlash(filepath.Dir(rel))
		// pkg/task declares Delete; it cannot import pkg/goal and is the store
		// the deleters call, not a delete SURFACE in the sense under guard.
		if pkgDir == "pkg/task" {
			return nil
		}
		file, pErr := parser.ParseFile(fset, path, nil, 0)
		if pErr != nil {
			return pErr
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if consumesTwoValueDelete(fn) {
				out[pkgDir+"."+funcKey(fn)] = callsGoalDeleteCleanup(fn)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk pkg/: %v", walkErr)
	}
	if len(out) == 0 {
		t.Fatal("the scanner found NO two-value Delete consumers at all — it has stopped working, " +
			"and a guard that guards nothing passes forever")
	}
	return out
}

// consumesTwoValueDelete reports whether fn's body (including nested function
// literals) assigns the two results of some `X.Delete(...)` call.
func consumesTwoValueDelete(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 2 || len(assign.Rhs) != 1 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == "Delete" {
			found = true
			return false
		}
		return true
	})
	return found
}

// callsGoalDeleteCleanup reports whether fn's body calls the shared
// task-delete -> goal-removal function under either reachable spelling.
func callsGoalDeleteCleanup(fn *ast.FuncDecl) bool {
	called := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if goalDeleteCleanupNames[exprName(call.Fun)] {
			called = true
			return false
		}
		return true
	})
	return called
}
