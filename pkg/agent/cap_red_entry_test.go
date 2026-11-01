package agent

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestCapRED_AdmissionView_SystemWakeEntryBoundary(t *testing.T) {
	for _, failConsumption := range []bool{false, true} {
		t.Run(map[bool]string{false: "body_running", true: "consume_fails"}[failConsumption], func(t *testing.T) {
			high, ok := false, true
			capRedMemory(t, &high, &ok)
			al, _ := newSteerAL(t)
			wireSteerCompletionDeps(t, al)
			provider, release := installParkedProvider(t, al)
			parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
			child := capRedLaunchChild(t, al, parent, "wake-boundary")
			messageID := appendWakeInboxEntry(t, al, child.SessionID, "worker finished")
			seen := false
			dispatchStateWriteTestHook = func(id string, _ int) {
				if id != child.SessionID {
					return
				}
				seen = true
				claim := capRedClaim(t, al, id)
				v := capRedAdmissionView(t, al, claim, false)
				capRedAssert(t, "S49a pre-commit wake has reservation only", []any{v.state, v.present}, []any{"starting", true})
				text := capRedStatus(t, al, parent, id)
				if !strings.HasPrefix(text, "starting,") {
					t.Errorf("S49a pre-entry status: %q", text)
				}
				if failConsumption {
					blocked := filepath.Join(t.TempDir(), "file-not-directory")
					if err := os.WriteFile(blocked, []byte("blocked"), 0o600); err != nil {
						t.Fatal(err)
					}
					al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(blocked, "inbox")), al.GetSessionLifecycleStore())
				}
			}
			t.Cleanup(func() { dispatchStateWriteTestHook = nil })
			done := make(chan struct{})
			var wakeErr error
			go func() {
				defer close(done)
				_, wakeErr = al.processSteeredSystemWake(context.Background(), wakeMessage(child.SessionID, messageID, child.Generation))
			}()
			defer func() {
				release()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Error("wake producer did not join after model release")
				}
			}()
			if failConsumption {
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("real wake consumption failure did not return")
				}
				if wakeErr == nil || !strings.Contains(wakeErr.Error(), "acknowledge") {
					t.Errorf("S49a actual inbox I/O failure not propagated: %v", wakeErr)
				}
				capRedAssert(t, "S49a failed entry cleared reservation", al.steerAdmission().activeCount(), 0)
				r, err := al.GetSessionLifecycleStore().Load(child.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				if !r.Terminal() {
					t.Errorf("S49a pre-boundary failure must be terminal, got %s", r.State)
				}
				select {
				case <-provider.entered:
					t.Error("failed wake entered its body")
				default:
				}
			} else {
				select {
				case <-provider.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("wake body did not reach real model edge")
				}
				v := capRedAdmissionView(t, al, capRedClaim(t, al, child.SessionID), false)
				capRedAssert(t, "S49a running before body completes", []any{v.state, v.present}, []any{"running", true})
				if text := capRedStatus(t, al, parent, child.SessionID); !strings.HasPrefix(text, "running,") {
					t.Errorf("S49a running body status: %q", text)
				}
			}
			if !seen {
				t.Error("real wake producer never crossed existing pre-commit boundary")
			}
		})
	}
}

// The live task-body case is paired with this structural boundary test. It
// specifically kills the r5 marker-after-return defect even if goroutine
// scheduling would let the dispatch caller return before the test can pause it.
// This asserts the r6-required call site, not a replacement implementation.
func TestCapRED_AdmissionView_TaskMarkerPrecedesWorkerSpawn(t *testing.T) {
	capRedMarkerOrder(t, "task_executor.go", "dispatchLaunchedTask", "persistTaskSessionBinding", "runTaskFromInProgress")
}
func TestCapRED_AdmissionView_WakeMarkerAfterConsumptionBeforeBody(t *testing.T) {
	capRedMarkerOrder(t, "loop_inbound.go", "processSteeredSystemWake", "consumeAdmittedWake", "runSteeredTurnBody")
}
func capRedMarkerOrder(t *testing.T, file, name, prepare, body string) {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate real production source")
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(filepath.Dir(self), file), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDecl
	for _, d := range f.Decls {
		if v, ok := d.(*ast.FuncDecl); ok && v.Name.Name == name {
			fn = v
		}
	}
	if fn == nil {
		t.Fatalf("real producer %s missing", name)
	}
	var prep, mark, run token.Pos
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if _, nested := n.(*ast.FuncLit); nested {
			return false
		}
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		s, ok := c.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch s.Sel.Name {
		case prepare:
			prep = c.Pos()
		case "markExecutionStarted":
			mark = c.Pos()
		case body:
			run = c.Pos()
		}
		return true
	})
	if prep == token.NoPos || run == token.NoPos {
		t.Fatalf("real %s preparation/body sites missing", name)
	}
	if mark == token.NoPos || !(prep < mark && mark < run) {
		t.Errorf("CAP-B10/S49d real %s must mark its full claim after %s and before %s; source positions prepare=%d marker=%d body=%d", name, prepare, body, prep, mark, run)
	}
}
