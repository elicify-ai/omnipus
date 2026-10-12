package agent

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// S1 final-gate M3 (a): a lifecycle record that cannot be READ is not "not a
// task run". The delivery must refuse before it queues or cancels anything.
func TestDeliverExternalCLIInstruction_UnreadableLifecycleRecordRefusesBeforeQueueOrCancel(t *testing.T) {
	al := u5bLoop(t)
	dir := t.TempDir()
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), session.NewLifecycleStore(dir))
	const sessKey = "sess-m3-unreadable"
	// A directory where the record file belongs: Load fails with a real read
	// error, not ErrLifecycleNotFound.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, sessKey+".jsonl"), 0o700))
	sess := u5bSeedLiveHolder(t, al, sessKey, 1)
	canceled := 0
	sess.mu.Lock()
	sess.cancelRun = func() { canceled++ }
	sess.mu.Unlock()

	_, err := al.DeliverExternalCLIInstruction(context.Background(), sessKey, "worker",
		providers.Message{Role: "user", Content: "S"}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "could not read the lifecycle record",
		"the refusal must name the unreadable record, not fall through to a later generic failure")
	require.Equal(t, 0, canceled, "the live run must not be interrupted when the delivery is refused")
	require.Equal(t, 0, al.pendingSteeringCountForScope(sessKey), "nothing may be queued")
}

// S1 final-gate M3 (b): the durable enqueue (steer receipt append + lifecycle
// read) must not run while the external-run holder lock is held. Structural
// proof: in deliverExternalCLIInstruction no call to the enqueue appears between
// a sess.mu.Lock() and its Unlock(), and the lock is not deferred over the
// enqueue.
func TestDeliverExternalCLIInstruction_EnqueueRunsOutsideTheHolderLock(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "external_run_delivery.go", nil, 0)
	require.NoError(t, err)
	var fn *ast.FuncDecl
	for _, d := range file.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == "deliverExternalCLIInstruction" {
			fn = f
		}
	}
	require.NotNil(t, fn, "instrument check: deliverExternalCLIInstruction not found")

	// Walk the function's TOP-LEVEL statements in order, tracking whether the
	// holder lock is held: an expression statement Lock() acquires, Unlock()
	// releases, and a deferred Unlock() keeps it held to the end. Branch-local
	// early-return unlocks are inside nested blocks and do not count.
	held, enqueues, heldAtEnqueue := false, 0, 0
	callName := func(e ast.Expr) string {
		if c, ok := e.(*ast.CallExpr); ok {
			if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
				return sel.Sel.Name
			}
		}
		return ""
	}
	// R1 delta T7 strengthening (S1-15): the direct name match above only sees a
	// call spelled "…Enqueue…" at THIS level. A helper called under the lock that
	// wraps the enqueue would slip past it. The package call graph closes that:
	// a call counts as an enqueue if it can reach the durable enqueue through ANY
	// chain of package calls, so the structural check no longer depends on what
	// the helper happens to be named.
	graph := agentPackageCallGraph(t)
	for _, st := range fn.Body.List {
		switch x := st.(type) {
		case *ast.ExprStmt:
			switch callName(x.X) {
			case "Lock":
				held = true
			case "Unlock":
				held = false
			}
		case *ast.DeferStmt:
			if callName(x.Call) == "Unlock" {
				held = true
			}
		}
		found := false
		ast.Inspect(st, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if name := callName(c); name != "" && reachesDurableEnqueue(name, graph) {
					found = true
				}
			}
			return true
		})
		if found {
			enqueues++
			if held {
				heldAtEnqueue++
			}
		}
	}
	require.NotZero(t, enqueues,
		"instrument check: the walk must find the enqueue call (directly or through a helper)")
	require.Equal(t, 0, heldAtEnqueue, "the durable enqueue must not run under the holder lock")

	// R1 delta T7: the walk above catches a DIRECT enqueue under the lock, but a
	// HELPER called under the lock could enqueue for it — the enqueue would then
	// be structurally outside the lock while behaviourally inside it. Assert the
	// locked region holds no call whose name looks like an enqueue, which covers
	// the delivery's own call AND any helper that wraps it.
	lockedEnqueueish := []string{}
	held = false
	for _, st := range fn.Body.List {
		switch x := st.(type) {
		case *ast.ExprStmt:
			switch callName(x.X) {
			case "Lock":
				held = true
			case "Unlock":
				held = false
			}
		case *ast.DeferStmt:
			if callName(x.Call) == "Unlock" {
				held = true
			}
		}
		if !held {
			continue
		}
		ast.Inspect(st, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				name := callName(c)
				if name == "" {
					return true
				}
				if strings.Contains(strings.ToLower(name), "enqueue") || reachesDurableEnqueue(name, graph) {
					lockedEnqueueish = append(lockedEnqueueish, name)
				}
			}
			return true
		})
	}
	require.Empty(t, lockedEnqueueish,
		"no enqueue may run under the holder lock, directly or through a helper: %v", lockedEnqueueish)
}

// agentPackageCallGraph maps every function/method defined in pkg/agent's
// non-test sources to the names of the functions/methods it calls. Names alone:
// the Go AST carries no type information without a full type-check, and for a
// structural guard the question is only "could this call, by any route, reach
// the enqueue".
func agentPackageCallGraph(t *testing.T) map[string][]string {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	require.NoError(t, err)
	graph := map[string][]string{}
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			// A source this guard cannot parse is not this guard's finding.
			continue
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var callees []string
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch f := c.Fun.(type) {
				case *ast.SelectorExpr:
					callees = append(callees, f.Sel.Name)
				case *ast.Ident:
					callees = append(callees, f.Name)
				}
				return true
			})
			graph[fn.Name.Name] = append(graph[fn.Name.Name], callees...)
		}
	}
	require.Contains(t, graph, "EnqueueSteeringMessageWithStatus",
		"instrument check: the call graph must contain the durable enqueue, or reachability proves nothing")
	return graph
}

// reachesDurableEnqueue reports whether name reaches
// EnqueueSteeringMessageWithStatus, directly or through any chain of package
// calls.
func reachesDurableEnqueue(name string, graph map[string][]string) bool {
	seen := map[string]bool{}
	queue := []string{name}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		if cur == "EnqueueSteeringMessageWithStatus" {
			return true
		}
		queue = append(queue, graph[cur]...)
	}
	return false
}
