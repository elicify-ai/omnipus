package browser

// tab_open_test.go — the contract for opening a brand-new tab, without a
// browser: the target is ACTIVATED before anything attaches (Chrome for Testing
// 153 does not run a new, un-activated tab's page — see openActivatedTarget),
// the whole open shares one budget, and a tab that failed to open is closed
// rather than left behind in Chrome.
//
// Durations appear only as ceilings that stop a broken implementation from
// hanging the test; every assertion is on what was called, in what order, with
// what arguments.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
)

// fakeTabExecutor records every browser-level CDP command it is given.
type fakeTabExecutor struct {
	mu     sync.Mutex
	calls  []string
	params []any

	createdID   target.ID
	createErr   error
	activateErr error
	closeErr    error
}

func (f *fakeTabExecutor) Execute(_ context.Context, method string, params, res any) error {
	f.mu.Lock()
	f.calls = append(f.calls, method)
	f.params = append(f.params, params)
	f.mu.Unlock()
	switch method {
	case target.CommandCreateTarget:
		if f.createErr != nil {
			return f.createErr
		}
		out, ok := res.(*target.CreateTargetReturns)
		if !ok {
			return errors.New("fake: unexpected result type for Target.createTarget")
		}
		out.TargetID = f.createdID
		return nil
	case target.CommandActivateTarget:
		return f.activateErr
	case target.CommandCloseTarget:
		return f.closeErr
	}
	return errors.New("fake: unexpected command " + method)
}

func (f *fakeTabExecutor) snapshot() ([]string, []any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...), append([]any(nil), f.params...)
}

var _ cdp.Executor = (*fakeTabExecutor)(nil)

func assertCalls(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("browser-level commands = %v, want %v", got, want)
	}
}

func TestOpenActivatedTarget_ActivatesTheNewTabBeforeReturningIt(t *testing.T) {
	exec := &fakeTabExecutor{createdID: "T-new"}

	id, err := openActivatedTarget(context.Background(), exec, "")
	if err != nil {
		t.Fatalf("openActivatedTarget: %v", err)
	}
	if id != "T-new" {
		t.Fatalf("returned target id = %q, want the one Chrome created (%q)", id, "T-new")
	}

	calls, params := exec.snapshot()
	assertCalls(t, calls, target.CommandCreateTarget, target.CommandActivateTarget)
	create, ok := params[0].(*target.CreateTargetParams)
	if !ok {
		t.Fatalf("createTarget params type = %T", params[0])
	}
	if create.URL != "about:blank" {
		t.Errorf("new tab URL = %q, want about:blank", create.URL)
	}
	if create.BrowserContextID != "" {
		t.Errorf("no browser context was given, but createTarget carried %q", create.BrowserContextID)
	}
	activate, ok := params[1].(*target.ActivateTargetParams)
	if !ok {
		t.Fatalf("activateTarget params type = %T", params[1])
	}
	if activate.TargetID != "T-new" {
		t.Errorf("activated target %q, want the tab just created (%q)", activate.TargetID, "T-new")
	}
}

func TestOpenActivatedTarget_CreatesTheTabInTheParentsBrowserContext(t *testing.T) {
	exec := &fakeTabExecutor{createdID: "T-new"}

	if _, err := openActivatedTarget(context.Background(), exec, "ctx-7"); err != nil {
		t.Fatalf("openActivatedTarget: %v", err)
	}
	_, params := exec.snapshot()
	create, ok := params[0].(*target.CreateTargetParams)
	if !ok {
		t.Fatalf("createTarget params type = %T", params[0])
	}
	if create.BrowserContextID != "ctx-7" {
		t.Fatalf("createTarget browser context = %q, want %q", create.BrowserContextID, "ctx-7")
	}
}

func TestOpenActivatedTarget_ActivationFailureClosesTheCreatedTab(t *testing.T) {
	exec := &fakeTabExecutor{createdID: "T-new", activateErr: errors.New("activate refused")}

	id, err := openActivatedTarget(context.Background(), exec, "")
	if err == nil {
		t.Fatal("a tab that could not be activated must not be returned as opened")
	}
	if id != "" {
		t.Errorf("returned id %q alongside an error", id)
	}
	if !strings.Contains(err.Error(), "activate refused") {
		t.Errorf("error must carry the cause; got %q", err)
	}

	calls, params := exec.snapshot()
	assertCalls(t, calls, target.CommandCreateTarget, target.CommandActivateTarget, target.CommandCloseTarget)
	closeParams, ok := params[2].(*target.CloseTargetParams)
	if !ok {
		t.Fatalf("closeTarget params type = %T", params[2])
	}
	if closeParams.TargetID != "T-new" {
		t.Fatalf("closed target %q, want the tab that failed to activate (%q)", closeParams.TargetID, "T-new")
	}
}

func TestOpenActivatedTarget_CreateFailureSendsNothingElse(t *testing.T) {
	exec := &fakeTabExecutor{createErr: errors.New("no more tabs")}

	if _, err := openActivatedTarget(context.Background(), exec, ""); err == nil {
		t.Fatal("expected the create failure to be returned")
	}
	calls, _ := exec.snapshot()
	assertCalls(t, calls, target.CommandCreateTarget)
}

// fakeSteps records what openNewTab asked of each step.
type fakeSteps struct {
	mu          sync.Mutex
	attached    []target.ID
	closed      []target.ID
	attachCtx   context.Context
	attachErr   error
	attachHangs bool
}

func (f *fakeSteps) steps(open func(context.Context) (target.ID, error)) newTabSteps {
	return newTabSteps{
		open: open,
		attach: func(id target.ID) (context.Context, context.CancelFunc, func() error) {
			ctx, cancel := context.WithCancel(context.Background())
			f.mu.Lock()
			f.attached = append(f.attached, id)
			f.attachCtx = ctx
			f.mu.Unlock()
			return ctx, cancel, func() error {
				if f.attachHangs {
					<-ctx.Done() // a tab that never answers, until the caller gives up
					return ctx.Err()
				}
				return f.attachErr
			}
		},
		close: func(id target.ID) {
			f.mu.Lock()
			f.closed = append(f.closed, id)
			f.mu.Unlock()
		},
	}
}

func (f *fakeSteps) record() (attached, closed []target.ID, attachCtx context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]target.ID(nil), f.attached...), append([]target.ID(nil), f.closed...), f.attachCtx
}

func openReturns(id target.ID) func(context.Context) (target.ID, error) {
	return func(context.Context) (target.ID, error) { return id, nil }
}

// runOpenNewTab calls openNewTab with a ceiling, so a broken implementation
// fails the test instead of hanging it.
func runOpenNewTab(t *testing.T, budget time.Duration, steps newTabSteps) (context.Context, context.CancelFunc, error) {
	t.Helper()
	type result struct {
		ctx    context.Context
		cancel context.CancelFunc
		err    error
	}
	done := make(chan result, 1)
	go func() {
		ctx, cancel, err := openNewTab(context.Background(), budget, steps)
		done <- result{ctx, cancel, err}
	}()
	select {
	case r := <-done:
		return r.ctx, r.cancel, r.err
	case <-time.After(10 * time.Second):
		t.Fatalf("openNewTab did not return within 10s on a %s budget — the open is not bounded", budget)
		return nil, nil, nil
	}
}

func TestOpenNewTab_AttachThatNeverCompletes_ClosesTheTabAndNamesTheBudget(t *testing.T) {
	f := &fakeSteps{attachHangs: true}
	const budget = 50 * time.Millisecond

	ctx, cancel, err := runOpenNewTab(t, budget, f.steps(openReturns("T1")))

	var timeout *tabOpenTimeoutError
	if !errors.As(err, &timeout) || timeout.phase != "attach" {
		t.Fatalf("want an attach timeout, got %v", err)
	}
	if want := "browser: timed out after 50ms waiting for the browser to attach the tab (target may be unresponsive)"; err.Error() != want {
		t.Errorf("error = %q, want %q (the message operators already quote, naming the whole budget)", err, want)
	}
	if ctx != nil || cancel != nil {
		t.Error("a failed open must not hand back a tab context")
	}
	attached, closed, attachCtx := f.record()
	if len(attached) != 1 || attached[0] != "T1" {
		t.Fatalf("attached = %v, want exactly the opened target T1", attached)
	}
	if len(closed) != 1 || closed[0] != "T1" {
		t.Fatalf("closed = %v, want T1 closed exactly once — a tab whose attach never completed must not stay open in Chrome", closed)
	}
	if attachCtx.Err() == nil {
		t.Error("the abandoned attach's context must be cancelled so its in-flight CDP calls unwind")
	}
}

func TestOpenNewTab_AttachError_ClosesTheTabAndReturnsTheCause(t *testing.T) {
	cause := errors.New("target detached during attach")
	f := &fakeSteps{attachErr: cause}

	_, _, err := runOpenNewTab(t, time.Minute, f.steps(openReturns("T2")))

	if !errors.Is(err, cause) {
		t.Fatalf("want the attach cause returned unchanged, got %v", err)
	}
	_, closed, _ := f.record()
	if len(closed) != 1 || closed[0] != "T2" {
		t.Fatalf("closed = %v, want T2 closed exactly once", closed)
	}
}

func TestOpenNewTab_Success_KeepsTheTabOpen(t *testing.T) {
	f := &fakeSteps{}

	ctx, cancel, err := runOpenNewTab(t, time.Minute, f.steps(openReturns("T3")))
	if err != nil {
		t.Fatalf("openNewTab: %v", err)
	}
	defer cancel()

	_, closed, attachCtx := f.record()
	if len(closed) != 0 {
		t.Fatalf("closed = %v on a successful open — the tab must stay open", closed)
	}
	if ctx != attachCtx {
		t.Error("the returned context must be the attached tab's own context")
	}
	if ctx.Err() != nil {
		t.Error("a successfully opened tab's context must still be live")
	}
}

func TestOpenNewTab_OpenFailure_NeverAttachesOrCloses(t *testing.T) {
	cause := errors.New("create refused")
	f := &fakeSteps{}

	_, _, err := runOpenNewTab(t, time.Minute, f.steps(func(context.Context) (target.ID, error) {
		return "", cause
	}))

	if !errors.Is(err, cause) {
		t.Fatalf("want the open cause returned, got %v", err)
	}
	var timeout *tabOpenTimeoutError
	if errors.As(err, &timeout) {
		t.Errorf("an open that failed well inside its budget must not be reported as a timeout: %v", err)
	}
	attached, closed, _ := f.record()
	if len(attached) != 0 || len(closed) != 0 {
		t.Fatalf("attached=%v closed=%v — nothing exists to attach to or close", attached, closed)
	}
}

func TestOpenNewTab_OpenThatOutlivesTheBudget_IsReportedAsATimeout(t *testing.T) {
	f := &fakeSteps{}
	const budget = 50 * time.Millisecond

	_, _, err := runOpenNewTab(t, budget, f.steps(func(ctx context.Context) (target.ID, error) {
		<-ctx.Done() // a browser that never answers Target.createTarget
		return "", ctx.Err()
	}))

	var timeout *tabOpenTimeoutError
	if !errors.As(err, &timeout) || timeout.phase != "open" {
		t.Fatalf("want an open timeout, got %v", err)
	}
	if !strings.Contains(err.Error(), "timed out after 50ms waiting for the browser to open the tab") {
		t.Errorf("error must name the budget and the stuck step; got %q", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the underlying deadline error must stay in the chain; got %v", err)
	}
	attached, _, _ := f.record()
	if len(attached) != 0 {
		t.Fatalf("attached = %v after the open never produced a target", attached)
	}
}
