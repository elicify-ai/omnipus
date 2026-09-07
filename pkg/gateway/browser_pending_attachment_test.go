package gateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

type attachmentWaitContext struct {
	context.Context
	once    sync.Once
	entered chan struct{}
}

func (c *attachmentWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

type attachmentWaitResult struct {
	snapshot browserAttachmentSnapshot
	err      error
}

func startAttachmentWait(t *testing.T, state *browserConnState, request browserAttachmentRequest) <-chan attachmentWaitResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	observed := &attachmentWaitContext{Context: ctx, entered: make(chan struct{})}
	result := make(chan attachmentWaitResult, 1)
	go func() {
		snapshot, err := state.awaitAttachment(observed, request)
		result <- attachmentWaitResult{snapshot, err}
	}()
	select {
	case <-observed.entered:
		return result
	case got := <-result:
		t.Fatalf("pending attachment returned before commit or cancellation: %+v", got)
	case <-ctx.Done():
		t.Fatal("attachment waiter did not enter its cancellation boundary")
	}
	return result
}

func readAttachmentWait(t *testing.T, result <-chan attachmentWaitResult) attachmentWaitResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(time.Second):
		t.Fatal("attachment waiter did not complete")
		return attachmentWaitResult{}
	}
}

func TestPendingAttachmentPreservesRequestLifetimeOnCommit(t *testing.T) {
	state := &browserConnState{}
	epoch := state.beginAttach()
	request := state.attachmentRequest()
	t.Cleanup(func() { state.clearAttachment() })
	if err := request.ctx.Err(); err != nil {
		t.Errorf("new attachment request is already canceled: %v", err)
	}
	mgr := &browser.BrowserManager{}
	if !state.bindAttachment(epoch, mgr, "chat", "panel") {
		t.Fatal("current attachment failed to bind")
	}
	got := state.commandAttachment()
	if got.ctx != request.ctx || request.ctx.Err() != nil || got.mgr != mgr || got.sessionID != "chat" || got.panelSessionID != "panel" {
		t.Fatalf("commit did not preserve original request identity: got=%+v request=%+v", got, request)
	}
}

func TestPendingAttachmentDoesNotExposePreviousCommandRoute(t *testing.T) {
	state := &browserConnState{}
	mgr := &browser.BrowserManager{}
	epoch := state.beginAttach()
	if !state.bindAttachment(epoch, mgr, "old-chat", "old-panel") {
		t.Fatal("fixture failed to bind")
	}
	old := state.commandAttachment()
	state.beginAttach()
	t.Cleanup(func() { state.clearAttachment() })
	if !errors.Is(old.ctx.Err(), context.Canceled) {
		t.Fatal("replacement retained old source lifetime")
	}
	got := state.commandAttachment()
	if got.mgr != nil || got.sessionID != "" || got.panelSessionID != "" {
		t.Fatalf("pending request inherited prior command route: %+v", got)
	}
}

func TestPendingAttachmentWaitsForItsExactCommit(t *testing.T) {
	state := &browserConnState{}
	epoch := state.beginAttach()
	request := state.attachmentRequest()
	t.Cleanup(func() { state.clearAttachment() })
	result := startAttachmentWait(t, state, request)
	mgr := &browser.BrowserManager{}
	if !state.bindAttachment(epoch, mgr, "chat", "panel") {
		t.Fatal("current request failed to commit")
	}
	got := readAttachmentWait(t, result)
	if got.err != nil || got.snapshot.ctx != request.ctx || got.snapshot.mgr != mgr || got.snapshot.sessionID != "chat" || got.snapshot.panelSessionID != "panel" {
		t.Fatalf("waiter did not receive exact committed route: %+v", got)
	}
}

func TestPendingAttachmentWaitRejectsReplacement(t *testing.T) {
	state := &browserConnState{}
	state.beginAttach()
	request := state.attachmentRequest()
	t.Cleanup(func() { state.clearAttachment() })
	result := startAttachmentWait(t, state, request)
	epoch := state.beginAttach()
	if !state.bindAttachment(epoch, &browser.BrowserManager{}, "replacement", "replacement-panel") {
		t.Fatal("replacement failed to commit")
	}
	got := readAttachmentWait(t, result)
	if !errors.Is(got.err, context.Canceled) || got.snapshot.mgr != nil {
		t.Fatalf("old waiter borrowed replacement route: %+v", got)
	}
}

func TestPendingAttachmentWaitUsesCallerDeadline(t *testing.T) {
	state := &browserConnState{}
	state.beginAttach()
	request := state.attachmentRequest()
	t.Cleanup(func() { state.clearAttachment() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan attachmentWaitResult, 1)
	go func() {
		got, err := state.awaitAttachment(ctx, request)
		result <- attachmentWaitResult{got, err}
	}()
	got := readAttachmentWait(t, result)
	if !errors.Is(got.err, context.DeadlineExceeded) || got.snapshot.mgr != nil || request.ctx.Err() != nil {
		t.Fatalf("caller timeout must end wait without ending attachment: snapshot=%+v err=%v attachment=%v", got.snapshot, got.err, request.ctx.Err())
	}
}

func TestPendingAttachmentFailureCancelsOnlyUncommittedRequest(t *testing.T) {
	for _, phase := range []string{"pending", "committed", "replaced"} {
		t.Run(phase, func(t *testing.T) {
			state := &browserConnState{}
			epoch := state.beginAttach()
			request := state.attachmentRequest()
			t.Cleanup(func() { state.clearAttachment() })
			switch phase {
			case "committed":
				if !state.bindAttachment(epoch, &browser.BrowserManager{}, "chat", "panel") {
					t.Fatal("fixture failed to bind")
				}
			case "replaced":
				state.beginAttach()
				request = state.attachmentRequest()
			}
			state.abandonAttachment(epoch)
			if phase == "pending" {
				if !errors.Is(request.ctx.Err(), context.Canceled) || state.bindAttachment(epoch, &browser.BrowserManager{}, "late", "late-panel") {
					t.Fatal("failed pending attachment could still commit")
				}
			} else if request.ctx.Err() != nil {
				t.Fatalf("stale failure canceled %s attachment: %v", phase, request.ctx.Err())
			}
		})
	}
}

func TestPendingAttachmentTakingPreviousRoutePreservesRequest(t *testing.T) {
	state := &browserConnState{}
	oldManager := &browser.BrowserManager{}
	if !state.bindAttachment(state.beginAttach(), oldManager, "old-chat", "old-panel") {
		t.Fatal("fixture failed to bind")
	}
	epoch := state.beginAttach()
	request := state.attachmentRequest()
	t.Cleanup(func() { state.clearAttachment() })
	mgr, chat, panel, current := state.takePreviousAttachment(epoch)
	if !current || mgr != oldManager || chat != "old-chat" || panel != "old-panel" || request.ctx.Err() != nil {
		t.Fatalf("taking previous route canceled or lost the new request: manager=%p chat=%q panel=%q current=%t err=%v", mgr, chat, panel, current, request.ctx.Err())
	}
	if mgr, chat, panel := state.attachment(); mgr != nil || chat != "" || panel != "" {
		t.Fatal("previous route remained installed after detach handoff")
	}
}

func TestPendingAttachmentStaleWorkCannotTakeReplacementRoute(t *testing.T) {
	state := &browserConnState{}
	oldEpoch := state.beginAttach()
	currentEpoch := state.beginAttach()
	mgr := &browser.BrowserManager{}
	if !state.bindAttachment(currentEpoch, mgr, "current-chat", "current-panel") {
		t.Fatal("fixture failed to bind")
	}
	t.Cleanup(func() { state.clearAttachment() })
	before := state.commandAttachment()
	if taken, _, _, current := state.takePreviousAttachment(oldEpoch); current || taken != nil {
		t.Errorf("stale work took the replacement route: manager=%p current=%t", taken, current)
	}
	if after := state.commandAttachment(); after != before || before.ctx.Err() != nil {
		t.Fatalf("stale work changed replacement route/lifetime: before=%+v after=%+v err=%v", before, after, before.ctx.Err())
	}
}

func TestPendingAttachmentInvalidFrameCancelsRequest(t *testing.T) {
	wc, state := newTabActionTestFixtures(t)
	epoch := state.beginAttach()
	request := state.attachmentRequest()
	t.Cleanup(func() { state.clearAttachment() })
	h := &BrowserWSHandler{}
	h.handleAttach(wc, state, "viewer", "user", []byte(`{`), nil, epoch)
	if mgr, chat, panel := state.attachment(); mgr != nil || chat != "" || panel != "" {
		t.Errorf("failed replacement retained previous route: manager=%p chat=%q panel=%q", mgr, chat, panel)
	}
	if !errors.Is(request.ctx.Err(), context.Canceled) {
		t.Fatalf("failed attach handler left request alive: %v", request.ctx.Err())
	}
	if state.bindAttachment(epoch, &browser.BrowserManager{}, "late", "late-panel") {
		t.Fatal("failed handler's request could still commit")
	}
}

func TestPendingAttachmentStaleInvalidFrameCannotPublish(t *testing.T) {
	wc, state := newTabActionTestFixtures(t)
	oldEpoch := state.beginAttach()
	currentEpoch := state.beginAttach()
	if !state.bindAttachment(currentEpoch, &browser.BrowserManager{}, "new-chat", "new-panel") {
		t.Fatal("fixture failed to bind replacement")
	}
	t.Cleanup(func() { state.clearAttachment() })
	before := state.commandAttachment()
	h := &BrowserWSHandler{}
	h.handleAttach(wc, state, "viewer", "user", []byte(`{`), nil, oldEpoch)
	if after := state.commandAttachment(); after != before || before.ctx.Err() != nil {
		t.Fatalf("stale malformed work changed replacement: before=%+v after=%+v", before, after)
	}
	select {
	case frame := <-wc.sendCh:
		t.Fatalf("stale malformed work published status to replacement: %s", frame)
	default:
	}
}
