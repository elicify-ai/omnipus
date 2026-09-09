package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/gorilla/websocket"
)

const browserCommandCapacity = 512

// browserCommandQueue keeps discrete gestures ordered without making the
// socket reader wait for Chrome. Only adjacent pointer moves are replaceable.
type browserCommandQueue struct { // not-wire-format: connection-local mutex, cancellation and job queue; never serialized.
	mu               sync.Mutex
	closed           bool
	running          bool
	jobs             []browserCommand
	activeCancel     context.CancelFunc
	activeNavigation bool
}

type browserCommand struct { // not-wire-format: queued execution closure with admission timing; never marshaled as a command payload.
	enqueued   time.Time
	move       bool
	navigation bool
	run        func(context.Context)
	onDiscard  func()
}

func (q *browserCommandQueue) submit(wg *sync.WaitGroup, job browserCommand) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	job.enqueued = time.Now()
	if job.move && len(q.jobs) > 0 && q.jobs[len(q.jobs)-1].move {
		q.jobs[len(q.jobs)-1] = job
		return true
	}
	if len(q.jobs) >= browserCommandCapacity {
		return false
	}
	if job.navigation && q.activeNavigation && q.activeCancel != nil {
		q.activeCancel()
	}
	q.jobs = append(q.jobs, job)
	if !q.running {
		q.running = true
		wg.Add(1)
		go q.drain(wg)
	}
	return true
}

func (q *browserCommandQueue) drain(wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		q.mu.Lock()
		if q.closed || len(q.jobs) == 0 {
			q.running = false
			q.activeCancel = nil
			q.activeNavigation = false
			q.mu.Unlock()
			return
		}
		job := q.jobs[0]
		q.jobs[0] = browserCommand{}
		q.jobs = q.jobs[1:]
		ctx, cancel := context.WithDeadline(context.Background(), job.enqueued.Add(5*time.Second))
		q.activeCancel = cancel
		q.activeNavigation = job.navigation
		q.mu.Unlock()
		job.run(ctx)
		cancel()
	}
}

func (q *browserCommandQueue) close() {
	q.mu.Lock()
	q.closed = true
	jobs := q.jobs
	q.jobs = nil
	if q.activeCancel != nil {
		q.activeCancel()
	}
	q.mu.Unlock()
	notifyDiscardedBrowserCommands(jobs)
}

func (q *browserCommandQueue) discard() {
	q.mu.Lock()
	jobs := q.jobs
	q.jobs = nil
	if q.activeCancel != nil {
		q.activeCancel()
	}
	q.mu.Unlock()
	notifyDiscardedBrowserCommands(jobs)
}

func notifyDiscardedBrowserCommands(jobs []browserCommand) {
	for _, job := range jobs {
		if job.onDiscard != nil {
			job.onDiscard()
		}
	}
}

func (h *BrowserWSHandler) dispatchBrowserCommand(wc *browserWSConn, state *browserConnState, viewerID, userID string, data []byte, typ string, cfg *config.Config, arrival ...time.Time) {
	attachment := state.commandAttachment()
	var in generated.BrowserInputFrame
	if typ == string(generated.WsFrameTypeBrowserInput) {
		if err := json.Unmarshal(data, &in); err != nil {
			wc.sendCriticalScopedGen(operationErrorStatus(attachment.sessionID, "invalid browser input"), dropContext("", viewerID, "input-invalid"), attachment.ctx, nil)
			return
		}
	}
	var received time.Time
	if len(arrival) > 0 {
		received = arrival[0]
	}
	var probe *browserInputTiming
	if h.inputTimingEnabled {
		probe = newBrowserInputTiming(true, &wc.inputTimingSequence, in, received)
	}
	if probe != nil {
		probe.mark("queue_submit")
	}
	job := browserCommand{
		move:       in.Kind == "mouse_move",
		navigation: in.Kind == "navigate" || in.Kind == "navigate_back" || in.Kind == "reload",
		run: func(ctx context.Context) {
			if probe != nil {
				probe.mark("queue_started")
				defer probe.finish()
			}

			if attachment.ctx.Err() != nil {
				return
			}
			commandCtx, cancel := attachment.bindContext(ctx)
			defer cancel()
			if ctx.Err() != nil {
				failBrowserInput(wc, state, viewerID, "Browser input expired; reconnect and retry.")
				return
			}
			switch typ {
			case string(generated.WsFrameTypeBrowserInput):
				h.handleInputContext(commandCtx, wc, state, attachment, viewerID, data, probe)
			case string(generated.WsFrameTypeBrowserControl):
				h.handleControlContext(commandCtx, wc, state, attachment, viewerID, userID, data, cfg)
			case string(generated.WsFrameTypeBrowserTabAction):
				h.handleTabActionContext(commandCtx, wc, state, attachment, viewerID, data)
			}
		},
	}
	if probe != nil {
		job.onDiscard = func() { probe.outcome = "queue_discarded"; probe.finish() }
	}
	if !state.commands.submit(&h.activeConns, job) {
		if probe != nil {
			probe.outcome = "queue_rejected"
			probe.finish()
		}
		failBrowserInput(wc, state, viewerID, "Browser input overloaded; reconnect and retry.")
	}
}

// Fail the affected viewer rather than replay uncertain input or discard a
// release while continuing its remaining gestures. Detach owns release cleanup.
func failBrowserInput(wc *browserWSConn, state *browserConnState, viewerID, reason string) {
	state.commands.close()
	slog.Warn("browser input connection reset", "viewer_id", viewerID, "reason", reason)
	if err := wc.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, reason), time.Now().Add(time.Second)); err != nil {
		slog.Debug("browser input reset: close notification failed", "viewer_id", viewerID, "error", err)
	}
	wc.close()
}
