// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_goal_terminal_guard_test.go is the enforcement half of review finding
// C1, and it exists because the FIRST fix for that finding was a comment.
//
// terminateTaskGoalRecord shipped with a doc comment stating "Call it from
// EVERY terminal disposition of a task. There are three". There were at least
// seven. Three of the four that were missed (handleTaskPatch,
// reconcileStuckTasks, and the two update_task tools) could not even have
// called the hook — it was unexported in pkg/agent, which pkg/gateway,
// pkg/tools and pkg/sysagent/tools cannot reach that way. A sentence asserting
// completeness is not a mechanism for completeness, and the sentence is
// precisely what stops the next reader going to look.
//
// A real chokepoint is impossible here. task.Store's own status-write path is
// the one place every writer must pass through, and it cannot call pkg/goal:
// pkg/goal imports pkg/task for the shared AcceptanceCriterion type, so the
// dependency only runs one way (task.Patch.Criteria's own doc comment and
// removeTaskGoalRecords' both already say so, for the two sibling problems).
// TerminateTaskGoalRecord is therefore one shared implementation with several
// call sites — and THIS test is what makes those call sites enforceable rather
// than remembered.
//
// It parses every non-test file under pkg/ and finds every function that
// writes task.Patch.Status — the one operation through which a task can reach
// a terminal status. That set must match the table below EXACTLY. Adding a new
// terminal writer therefore fails this test until its author either wires the
// hook or writes down why the writer cannot reach a terminal status.
package tools

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// goalHookNames are the call expressions that count as routing through the
// shared task-terminal -> goal-terminal hook. Three spellings because the hook
// is reached differently from each package: qualified from pkg/gateway and
// pkg/sysagent/tools, bare from inside pkg/tools, and through pkg/agent's own
// thin wrapper from pkg/agent.
var goalHookNames = map[string]bool{
	"tools.TerminateTaskGoalRecord": true,
	"TerminateTaskGoalRecord":       true,
	"terminateTaskGoalRecord":       true,
}

// statusWriter is one row of the registry: a function that writes
// task.Patch.Status, and the decision made about it.
type statusWriter struct {
	// where is "<pkg dir>.<function>", the key the scan produces.
	where string
	// mustHook is true when this writer can move a task to done/failed and
	// must therefore end the task's paired goal record.
	mustHook bool
	// why records the reason a writer is exempt from the hook. Required when
	// mustHook is false — an exemption with no stated reason is how the
	// original defect was built.
	why string
}

// knownStatusWriters is the closed registry. Every function under pkg/ that
// writes task.Patch.Status must appear here exactly once.
var knownStatusWriters = []statusWriter{
	// ---- the terminal writers: each must end the paired goal record ------
	{where: "pkg/agent.TaskExecutor.completeTaskWithResult", mustHook: true},
	{where: "pkg/agent.TaskExecutor.failTask", mustHook: true},
	{where: "pkg/agent.PlanEngine.cancelMemberLocked", mustHook: true},
	{where: "pkg/gateway.taskPatch.buildPatch", mustHook: true},
	{where: "pkg/gateway.restAPI.reconcileStuckTasks", mustHook: true},
	{where: "pkg/tools.taskUpdateToolExecute.buildPatchFields", mustHook: true},
	{where: "pkg/sysagent/tools.taskUpdateToolExecute.buildPatch", mustHook: true},

	// ---- non-terminal writers: each states why it cannot reach done/failed --
	{
		where: "pkg/agent.TaskExecutor.consumeTaskAttempt",
		why: "writes task.StatusNext only (the automatic fresh-run restart after a failed run). It " +
			"ends the run's goal so the fresh run can Goal.Reactivate it, WITHOUT the goal hook, because " +
			"the task itself has not ended — its one outcome line is written when it does. Its " +
			"attempts-exhausted branch delegates the terminal write to completeTaskWithResult, which " +
			"carries the hook.",
	},
	{
		where: "pkg/agent.PlanEngine.promoteInboxMembers",
		why:   "writes task.StatusNext only — inbox -> next promotion.",
	},
	{
		where: "pkg/gateway.restAPI.detachMemberOnPlanDelete",
		why: "writes task.StatusInbox only — detaching a member from a deleted plan. " +
			"Its illegal-transition fallback drops the Status field entirely and " +
			"patches plan_id alone, so a terminal member is never re-written here.",
	},
	{
		where: "pkg/gateway.taskPatch.launchIfStarted",
		why: "writes only a launch-failure rollback to the task's prior non-terminal status. " +
			"The path is entered only after a transition into in_progress, so the prior status cannot be terminal.",
	},
	{
		where: "pkg/tools.TaskRunTool.Execute",
		why: "writes task.StatusInProgress to launch, and reverts to the PRIOR " +
			"non-terminal status on a launch failure. A run that actually finishes " +
			"terminates through pkg/agent's TaskExecutor, which carries the hook.",
	},
	{
		where: "pkg/tools.SetTodosTool.archiveOtherScratchpadCards",
		why: "writes task.StatusDone, but ONLY to cards with Scratchpad == true " +
			"(the loop skips every non-scratchpad task). A scratchpad card has no " +
			"paired goal record by construction (GOAL-FR-023), so the hook would " +
			"resolve ErrOwnerNotFound and do nothing. If this filter is ever " +
			"widened to real create_task cards, this row must become mustHook.",
	},
}

// TestEveryTaskStatusWriterIsClassified is the guard.
//
// The oracle is deliberately a SET EQUALITY, not a subset check: a new writer
// that nobody classified fails just as loudly as a classified writer that lost
// its hook. A subset check would let the exact defect under review reappear —
// four writers existed, nobody had listed them, and every test passed.
func TestEveryTaskStatusWriterIsClassified(t *testing.T) {
	found := scanTaskStatusWriters(t)

	known := make(map[string]statusWriter, len(knownStatusWriters))
	for _, w := range knownStatusWriters {
		if _, dup := known[w.where]; dup {
			t.Fatalf("registry is malformed: %q is listed twice", w.where)
		}
		if !w.mustHook && strings.TrimSpace(w.why) == "" {
			t.Errorf("registry entry %q claims exemption from the goal hook with no stated reason.\n"+
				"An undocumented exemption is how review finding C1 was built: the original fix "+
				"asserted three writers existed, four more were never looked at, and nothing failed.",
				w.where)
		}
		known[w.where] = w
	}

	for where := range found {
		if _, ok := known[where]; !ok {
			t.Errorf("UNCLASSIFIED task.Patch.Status writer: %s\n"+
				"Every function that writes task.Patch.Status can potentially move a task to "+
				"done/failed, and a task reaching a terminal status MUST end its paired goal "+
				"record (GOAL-FR-015/FR-027/FR-028) — otherwise the record stays `active` forever "+
				"and the next run of that task silently skips Goal.Reactivate, inheriting the "+
				"previous run's attempts, rounds and per-criterion statuses.\n"+
				"Add a row to knownStatusWriters in this file: either mustHook (and call "+
				"tools.TerminateTaskGoalRecord when the write lands terminal), or an exemption "+
				"with a written reason why this writer cannot reach done/failed.", where)
		}
	}
	for where, w := range known {
		hooked, ok := found[where]
		if !ok {
			t.Errorf("registry entry %q no longer writes task.Patch.Status.\n"+
				"Either it was renamed/removed (delete the row) or the scanner stopped seeing it "+
				"(fix the scanner — a guard that silently stops guarding is worse than no guard).\n"+
				"What the scanner DID find: %v",
				where, sortedKeys(found))
			continue
		}
		switch {
		case w.mustHook && !hooked:
			t.Errorf("TERMINAL WRITER WITH NO GOAL HOOK: %s\n"+
				"This function writes task.Patch.Status and is registered as able to reach "+
				"done/failed, but its body never calls TerminateTaskGoalRecord. A task that "+
				"terminates without ending its paired goal record leaves the record `active` "+
				"forever; the next run of that task then takes activateTaskGoal's `default:` "+
				"branch, skipping Goal.Reactivate entirely — attempts, rounds, latest_reason, "+
				"both keeper budgets, active_session_id and every criterion status carry over "+
				"from the previous run, and a DoD item marked `met` last time is served as `met` "+
				"for work this run never did.", where)
		case !w.mustHook && hooked:
			t.Errorf("registry entry %q is marked exempt (%q) but DOES call the goal hook.\n"+
				"One of the two is wrong. If the writer can now reach a terminal status, flip "+
				"the row to mustHook; if the call is spurious, remove it.", where, w.why)
		}
	}
}

// scanTaskStatusWriters parses every non-test Go file under pkg/ and returns
// the functions that write task.Patch.Status, mapped to whether that same
// function also calls the goal hook.
//
// Two write shapes are recognised, because both appear in the real writers:
//
//	task.Patch{Status: &st, ...}   — a composite literal carrying the field
//	patch.Status = &st             — an assignment onto a local task.Patch
//
// Attribution is to the enclosing top-level function, so a write inside a
// closure (rest_tasks.go's buildLaunchRevertPatch is one) is reported against
// the function that owns it rather than vanishing.
func scanTaskStatusWriters(t *testing.T) map[string]bool {
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
			// pkg/task itself declares Patch; it cannot import pkg/goal and is
			// not a task-status *writer* in the sense under guard (it is the
			// store the writers call). pkg/api/generated is machine-written.
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
			return fmt.Errorf("relative path %s: %w", path, rErr)
		}
		pkgDir := filepath.ToSlash(filepath.Dir(rel))
		if pkgDir == "pkg/task" {
			return nil
		}
		file, pErr := parser.ParseFile(fset, path, nil, 0)
		if pErr != nil {
			return fmt.Errorf("parse %s: %w", path, pErr)
		}
		patchStateTypes := taskPatchStateTypes(file)
		receiverHooks := map[string]bool{}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Body != nil && callsGoalHook(fn) {
				receiverHooks[receiverTypeName(fn)] = true
			}
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			directWrite := writesPatchStatus(fn)
			stateWrite := writesReceiverPatchStatus(fn, patchStateTypes)
			if directWrite || stateWrite {
				out[pkgDir+"."+funcKey(fn)] = callsGoalHook(fn) || (stateWrite && receiverHooks[receiverTypeName(fn)])
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk pkg/: %v", walkErr)
	}
	if len(out) == 0 {
		t.Fatal("the scanner found NO task.Patch.Status writers at all — it has stopped working, " +
			"and a guard that guards nothing passes forever")
	}
	return out
}

// taskPatchStateTypes returns state structs that carry a task.Patch field.
// Extracted stage methods write receiver.patch.Status rather than a local
// task.Patch, so the guard must follow that typed field across the pipeline.
func taskPatchStateTypes(file *ast.File) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				if !isTaskPatchType(field.Type) {
					continue
				}
				if out[typeSpec.Name.Name] == nil {
					out[typeSpec.Name.Name] = map[string]bool{}
				}
				for _, name := range field.Names {
					out[typeSpec.Name.Name][name.Name] = true
				}
			}
		}
	}
	return out
}

func receiverTypeName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if ident, ok := typ.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func writesReceiverPatchStatus(fn *ast.FuncDecl, patchStateTypes map[string]map[string]bool) bool {
	fields := patchStateTypes[receiverTypeName(fn)]
	if len(fields) == 0 || fn.Recv == nil || len(fn.Recv.List[0].Names) == 0 {
		return false
	}
	receiverName := fn.Recv.List[0].Names[0].Name
	written := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, isAssign := n.(*ast.AssignStmt)
		if !isAssign {
			return true
		}
		for i, lhs := range assign.Lhs {
			status, isSelector := lhs.(*ast.SelectorExpr)
			if !isSelector || status.Sel.Name != "Status" {
				continue
			}
			if i < len(assign.Rhs) {
				if ident, isIdent := assign.Rhs[i].(*ast.Ident); isIdent && ident.Name == "nil" {
					continue // clearing Status prevents a write; it cannot transition the task
				}
			}
			patchField, isPatchField := status.X.(*ast.SelectorExpr)
			if !isPatchField || !fields[patchField.Sel.Name] {
				continue
			}
			base, isBase := patchField.X.(*ast.Ident)
			if isBase && base.Name == receiverName {
				written = true
				return false
			}
		}
		return true
	})
	return written
}

// funcKey renders a FuncDecl as "Receiver.Name" (or just "Name"), pointer
// receivers flattened — the shape a reader would use to find it.
func funcKey(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if ident, ok := typ.(*ast.Ident); ok {
		return ident.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// writesPatchStatus reports whether fn's body (including any nested function
// literal) writes the Status field of a task.Patch.
func writesPatchStatus(fn *ast.FuncDecl) bool {
	patchVars := patchLocals(fn)
	written := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if written {
			return false
		}
		switch node := n.(type) {
		case *ast.CompositeLit:
			if !isTaskPatchType(node.Type) {
				return true
			}
			for _, elt := range node.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Status" {
					written = true
					return false
				}
			}
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Status" {
					continue
				}
				base, ok := sel.X.(*ast.Ident)
				if ok && patchVars[base.Name] {
					written = true
					return false
				}
			}
		}
		return true
	})
	return written
}

// patchLocals collects the names of local variables declared as a task.Patch
// inside fn, so `patch.Status = &st` can be told apart from any other
// `.Status = ` assignment (session.MetaPatch and wireMount both have one).
func patchLocals(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			if node.Tok != token.DEFINE || len(node.Lhs) != len(node.Rhs) {
				return true
			}
			for i, rhs := range node.Rhs {
				lit, ok := rhs.(*ast.CompositeLit)
				if !ok || !isTaskPatchType(lit.Type) {
					continue
				}
				if ident, ok := node.Lhs[i].(*ast.Ident); ok {
					out[ident.Name] = true
				}
			}
		case *ast.DeclStmt:
			gen, ok := node.Decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				return true
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || !isTaskPatchType(vs.Type) {
					continue
				}
				for _, name := range vs.Names {
					out[name.Name] = true
				}
			}
		}
		return true
	})
	return out
}

// isTaskPatchType reports whether expr names the task.Patch type.
func isTaskPatchType(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Patch" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "task"
}

// callsGoalHook reports whether fn's body calls the shared task-terminal ->
// goal-terminal hook under any of its three reachable spellings.
func callsGoalHook(fn *ast.FuncDecl) bool {
	called := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if goalHookNames[exprName(call.Fun)] {
			called = true
			return false
		}
		return true
	})
	return called
}

// exprName renders `Foo` and `pkg.Foo` call targets as their source text, and
// anything else as "".
func exprName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if pkg, ok := e.X.(*ast.Ident); ok {
			return pkg.Name + "." + e.Sel.Name
		}
	}
	return ""
}

// sortedKeys is a small helper used by the failure messages above to keep
// output stable across runs.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
