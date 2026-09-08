package browser

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

const documentPaintTimeout = 15 * time.Second

// Each watch belongs to one attachment and target. Its work retains the exact
// capture/token; late browser events cannot discover and modify a replacement.
type liveDocumentWatch struct {
	lv          *LiveView
	ctx, target context.Context
	mu          sync.Mutex
	frameID     cdp.FrameID
	loaderID    cdp.LoaderID
	loadingID   cdp.LoaderID
	requestID   network.RequestID
	work        *liveDocumentWork
	events      chan liveDocumentEvent
	eventMu     sync.Mutex // serializes only the nonblocking queue producers
	observed    atomic.Uint64
	processed   atomic.Uint64
	failed      atomic.Bool
	cancel      context.CancelFunc
}

type liveDocumentEvent struct {
	sequence uint64
	value    any
}

type liveDocumentInitial struct {
	work     *liveDocumentWork
	frameID  cdp.FrameID
	loaderID cdp.LoaderID
}

type liveDocumentWork struct {
	cs       *CaptureSession
	token    *captureDocumentTransition
	loaderID cdp.LoaderID
	settling bool
	deadline time.Time
}

// Called under lv.mu. Listener registration does no browser I/O; initial
// frame discovery runs after this method returns.
func (lv *LiveView) installDocumentWatchLocked(listenCtx, targetCtx context.Context) {
	lv.documentWatch = nil
	if lv.mgr == nil || chromedp.FromContext(targetCtx) == nil {
		return
	}
	ctx, cancel := context.WithCancel(listenCtx)
	w := &liveDocumentWatch{lv: lv, ctx: ctx, target: targetCtx, cancel: cancel, events: make(chan liveDocumentEvent, 128)}
	lv.documentWatch = w
	chromedp.ListenTarget(ctx, w.enqueue)
	go func() {
		w.initialize()
		w.processEvents()
	}()
}

// Chrome invokes listeners on its protocol reader. Never acquire manager or
// capture locks there: a command holding such a lock may await that reader.
func (w *liveDocumentWatch) enqueue(event any) {
	switch e := event.(type) {
	case *page.EventFrameStartedNavigating, *page.EventFrameNavigated, *page.EventNavigatedWithinDocument, *network.EventLoadingFailed, *liveDocumentInitial:
	case *network.EventRequestWillBeSent:
		if e.Type != network.ResourceTypeDocument {
			return
		}
	default:
		return
	}
	if w.ctx.Err() != nil {
		return
	}
	w.eventMu.Lock()
	defer w.eventMu.Unlock()
	entry := liveDocumentEvent{sequence: w.observed.Add(1), value: event}
	select {
	case w.events <- entry:
	default:
		if w.failed.CompareAndSwap(false, true) {
			w.cancel()
			go w.emitFailure("The browser could not keep up with page changes. Reload the page or retry the browser connection.")
		}
	}
}

func (w *liveDocumentWatch) processEvents() {
	for {
		select {
		case <-w.ctx.Done():
			return
		case event := <-w.events:
			w.onEvent(event.value)
			w.processed.Store(event.sequence)
		}
	}
}

func (w *liveDocumentWatch) inputPending() bool {
	return w != nil && (w.failed.Load() || w.observed.Load() != w.processed.Load())
}

func (w *liveDocumentWatch) active() bool {
	if w.ctx.Err() != nil {
		return false
	}
	active, _, err := w.lv.mgr.activeTargetSnapshot(w.lv.sessionID)
	return err == nil && active == w.target
}

func (w *liveDocumentWatch) initialize() {
	ctx, cancel := context.WithTimeout(w.ctx, documentPaintTimeout)
	defer cancel()
	w.mu.Lock()
	work, beginErr := w.beginLocked("")
	w.mu.Unlock()
	if beginErr != nil {
		w.reportFailure(work, beginErr)
		return
	}
	var tree *page.FrameTree
	err := w.lv.runCDP(ctx, documentPaintTimeout, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		tree, err = page.GetFrameTree().Do(ctx)
		return err
	}))
	if err != nil {
		if work != nil || w.lv.mgr.CaptureSessionForPanel(w.lv.sessionID) == nil {
			w.reportFailure(work, err)
		}
		return
	}
	if tree == nil || tree.Frame == nil || !w.active() {
		return
	}
	w.mu.Lock()
	if w.frameID == "" {
		w.frameID, w.loaderID = tree.Frame.ID, tree.Frame.LoaderID
	}
	w.mu.Unlock()
	// Discover the main frame before consuming queued events, so subframes
	// can be ignored correctly. Complete this exact initial work only after
	// preceding browser events; a newer UI command also invalidates its token.
	w.enqueue(&liveDocumentInitial{work: work, frameID: tree.Frame.ID, loaderID: tree.Frame.LoaderID})
}

func (w *liveDocumentWatch) beginLocked(loader cdp.LoaderID) (*liveDocumentWork, error) {
	if !w.active() {
		return nil, ErrStaleCaptureFrame
	}
	cs := w.lv.mgr.CaptureSessionForPanel(w.lv.sessionID)
	if cs == nil {
		return nil, nil
	}
	active, target, err := w.lv.mgr.activeTargetSnapshot(w.lv.sessionID)
	if err != nil {
		return nil, err
	}
	if active != w.target {
		return nil, ErrStaleCaptureFrame
	}
	token, err := cs.beginDocumentTransitionWhen(string(target), func() bool {
		currentTarget := cs.frames.snapshot().Geometry.TargetID
		return w.ctx.Err() == nil && (currentTarget == "" || currentTarget == string(target))
	})
	if errors.Is(err, context.Canceled) {
		return nil, nil // A stopped capture cannot authorize old input.
	}
	if err != nil {
		return nil, err
	}
	work := &liveDocumentWork{cs: cs, token: token, loaderID: loader, deadline: time.Now().Add(documentPaintTimeout)}
	w.work = work
	go w.watchDeadline(work)
	return work, nil
}

func (w *liveDocumentWatch) watchDeadline(work *liveDocumentWork) {
	timer := time.NewTimer(time.Until(work.deadline))
	defer timer.Stop()
	select {
	case <-w.ctx.Done():
	case <-work.token.ctx.Done():
	case <-timer.C:
		w.reportFailure(work, fmt.Errorf("new document did not provide a confirmed picture in time"))
	}
}

func (w *liveDocumentWatch) onEvent(event any) {
	if !w.active() {
		return
	}
	w.mu.Lock()
	var work *liveDocumentWork
	var err error
	var paint bool
	switch e := event.(type) {
	case *liveDocumentInitial:
		if e.work != nil && w.work == e.work && w.owns(e.work) && !e.work.settling {
			work = e.work
			work.loaderID = e.loaderID
			paint = true
		}
	case *page.EventFrameStartedNavigating:
		if w.frameID != "" && e.FrameID == w.frameID {
			work, err = w.beginLocked(e.LoaderID)
		}
	case *network.EventRequestWillBeSent:
		if e.Type == network.ResourceTypeDocument && w.frameID != "" && e.FrameID == w.frameID {
			w.loadingID, w.requestID = e.LoaderID, e.RequestID
			work = w.work
			if work == nil || !work.cs.documentTransitionCurrent(work.token) {
				work, err = w.beginLocked(e.LoaderID)
			} else if work.loaderID == "" {
				work.loaderID = e.LoaderID
			}
		}
	case *network.EventLoadingFailed:
		work = w.work
		if work != nil && e.RequestID == w.requestID && work.loaderID == w.loadingID && work.cs.documentTransitionCurrent(work.token) {
			// Unlike frameStoppedLoading, the network failure identifies the
			// exact provisional document. An old request's failure cannot
			// reopen the page underneath a newer navigation.
			if !work.settling && w.frameID != "" {
				work.settling = true
				go w.settle(work, w.frameID, w.loaderID)
			}
		}
	case *page.EventFrameNavigated:
		if e.Frame == nil || e.Frame.ParentID != "" {
			break
		}
		w.frameID = e.Frame.ID
		work = w.work
		// A commit for an older provisional document cannot complete the
		// newer navigation that has already retired it.
		if work != nil && work.cs.documentTransitionCurrent(work.token) && work.loaderID != "" && work.loaderID != e.Frame.LoaderID {
			break
		}
		w.loaderID = e.Frame.LoaderID
		if work == nil || !work.cs.documentTransitionCurrent(work.token) {
			work, err = w.beginLocked(e.Frame.LoaderID)
		}
		if work != nil {
			work.loaderID = e.Frame.LoaderID
			paint = true
		}
	case *page.EventNavigatedWithinDocument:
		if e.FrameID == w.frameID && w.frameID != "" {
			work, err = w.beginLocked(w.loaderID)
			paint = work != nil
		}
	}
	frame := w.frameID
	if paint && !work.settling {
		work.settling = true
		go w.settle(work, frame, work.loaderID)
	}
	w.mu.Unlock()
	if err != nil && !errors.Is(err, ErrStaleCaptureFrame) {
		w.reportFailure(work, err)
	}
}

// Explicit navigation retires the old picture before the command reaches
// Chrome. URL validation has already succeeded. Absence of capture must not
// prevent navigation from being used to recover a broken browser panel.
func (lv *LiveView) beginInputDocument(targetCtx context.Context) (*liveDocumentWatch, *liveDocumentWork, error) {
	if lv.mgr == nil {
		return nil, nil, nil
	}
	lv.mu.Lock()
	w := lv.documentWatch
	if w != nil && w.failed.Load() && lv.listenCtx != nil {
		lv.installDocumentWatchLocked(lv.listenCtx, targetCtx)
		w = lv.documentWatch
	}
	lv.mu.Unlock()
	if w != nil && w.target == targetCtx && w.active() {
		w.mu.Lock()
		work, err := w.beginLocked("")
		w.mu.Unlock()
		return w, work, err
	}
	cs := lv.mgr.CaptureSessionForPanel(lv.sessionID)
	if cs == nil {
		return nil, nil, nil
	}
	_, target, err := lv.mgr.activeTargetSnapshot(lv.sessionID)
	if err != nil {
		return nil, nil, err
	}
	token, err := cs.beginDocumentTransition(string(target))
	if errors.Is(err, context.Canceled) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return nil, &liveDocumentWork{cs: cs, token: token}, nil
}

// Only a proven no-op or protocol refusal uses this path. A successful
// navigation acknowledgement is never sufficient to reopen input.
func (w *liveDocumentWatch) resumeUnchanged(work *liveDocumentWork) {
	if w == nil || work == nil {
		return
	}
	w.mu.Lock()
	if w.work == work && work.cs.documentTransitionCurrent(work.token) && !work.settling {
		work.settling = true
		if w.frameID == "" {
			go w.resumeUnchangedWithoutFrame(work)
		} else {
			go w.settle(work, w.frameID, w.loaderID)
		}
	}
	w.mu.Unlock()
}

func (w *liveDocumentWatch) resumeUnchangedWithoutFrame(work *liveDocumentWork) {
	ctx, cancel := context.WithDeadline(w.ctx, work.deadline)
	stop := context.AfterFunc(work.token.ctx, cancel)
	defer cancel()
	defer stop()
	var tree *page.FrameTree
	err := w.lv.runCDP(ctx, documentPaintTimeout, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		tree, err = page.GetFrameTree().Do(ctx)
		return err
	}))
	if err != nil {
		w.reportFailure(work, err)
		return
	}
	if tree != nil && tree.Frame != nil && w.owns(work) {
		w.settle(work, tree.Frame.ID, tree.Frame.LoaderID)
	}
}

func (w *liveDocumentWatch) owns(work *liveDocumentWork) bool {
	return w.active() && w.lv.mgr.CaptureSessionForPanel(w.lv.sessionID) == work.cs && work.cs.documentTransitionCurrent(work.token)
}

func (w *liveDocumentWatch) settle(work *liveDocumentWork, frame cdp.FrameID, loader cdp.LoaderID) {
	ctx, cancel := context.WithDeadline(w.ctx, work.deadline)
	stopToken := context.AfterFunc(work.token.ctx, cancel)
	defer cancel()
	defer stopToken()
	paint := documentPaintAction{frameID: frame, loaderID: loader}
	if !w.owns(work) {
		return
	}
	// Paint waits outside command admission so navigation and held-key
	// cleanup remain usable while the page is slow or changing.
	err := w.lv.runCDP(ctx, documentPaintTimeout, paint)
	if err != nil {
		w.reportFailure(work, err)
		return
	}
	var measured CaptureFrameState
	_, err = w.lv.withViewportAdmission(ctx, w.target, func(operation context.Context) (bool, error) {
		if !w.owns(work) {
			return false, ErrStaleCaptureFrame
		}
		geometry, err := w.lv.measureCaptureFrame(operation, work.cs)
		if err != nil {
			return false, err
		}
		if err := w.lv.runCDP(operation, viewportScaleTimeout, chromedp.ActionFunc(paint.checkDocument)); err != nil {
			return false, err
		}
		// Completion retires the token itself. Detach its cancellation hook
		// first; the capture's atomic token check still rejects supersession.
		if !stopToken() || ctx.Err() != nil || !w.owns(work) {
			return false, ErrStaleCaptureFrame
		}
		measured, err = work.cs.completeDocumentTransition(work.token, geometry.Width, geometry.Height, geometry.Scale)
		return err == nil, err
	}, func(operation context.Context) error {
		if !work.cs.RecaptureFrameContext(operation, measured) {
			return fmt.Errorf("browser live: document recapture was not admitted")
		}
		return nil
	})
	if err != nil {
		if measured.Generation != 0 && w.active() && w.lv.mgr.CaptureSessionForPanel(w.lv.sessionID) == work.cs {
			current := work.cs.FrameState()
			if current.CaptureID == measured.CaptureID && current.Generation == measured.Generation {
				w.reportFailure(nil, err)
			}
		} else {
			w.reportFailure(work, err)
		}
	}
}

func (w *liveDocumentWatch) reportFailure(work *liveDocumentWork, err error) {
	if !w.active() || work != nil && !w.owns(work) {
		return
	}
	logger.WarnCF("browser", "live view document refresh failed", map[string]any{"session_id": w.lv.sessionID, "error": err.Error()})
	w.emitFailure("The browser could not confirm the new page picture. Reload the page or retry the browser connection.")
}

func (w *liveDocumentWatch) emitFailure(message string) {
	w.lv.mu.Lock()
	current := w.lv.documentWatch == w
	sinks := w.lv.snapshotStatusSinksLocked()
	w.lv.mu.Unlock()
	if current {
		for _, sink := range sinks {
			sink(message)
		}
	}
}
