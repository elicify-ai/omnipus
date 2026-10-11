package agent

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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
			if c, ok := n.(*ast.CallExpr); ok && callName(c) == "EnqueueSteeringMessageWithStatus" {
				found = true
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
	require.Equal(t, 1, enqueues, "instrument check: exactly one enqueue call expected")
	require.Equal(t, 0, heldAtEnqueue, "the durable enqueue must not run under the holder lock")
}
