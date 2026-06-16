// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

const (
	// workerInboxCap is the buffered capacity of a session worker's inbox.
	// A depth of 8 covers rapid-fire follow-ups without blocking the dispatcher.
	workerInboxCap = 8
)

// workerIdleTimeout is the duration after which a worker self-exits when no
// messages have arrived. Declared as a var (not const) so tests can override
// it without waiting the full 60 s.
var workerIdleTimeout = 60 * time.Second

// sessionWorker is a per-scope (per-session) goroutine that processes inbound
// messages sequentially. Each session gets exactly one worker goroutine so
// turns within the session are ordered, while workers for different sessions
// run concurrently — fixing the original single-threaded Run() bottleneck.
type sessionWorker struct {
	// scope is the routing key ("agent:<id>:session:<sid>" etc.) that owns
	// this worker. Immutable after construction.
	scope string

	// inbox receives messages dispatched by Run().
	// Full → drop with WARN and notify user (see enqueue).
	inbox chan bus.InboundMessage

	// ctx is the worker's own context, derived from context.Background() so
	// the worker's lifetime is independent of Run()'s context.
	// AgentLoop.Close() uses cancel to stop the worker explicitly.
	ctx    context.Context
	cancel context.CancelFunc

	// done is closed by runLoop when it exits. Allows Close() to wait for
	// all workers to drain with a deadline.
	done chan struct{}

	// inTurn reports whether processTurn is currently executing. When true,
	// enqueue redirects same-scope messages into the in-turn steering queue
	// (so the agent auto-continues with the late-append text) rather than
	// the worker's own inbox (which would queue them as a fresh follow-up
	// turn after the current one finishes). Set/cleared by processTurn.
	inTurn atomic.Bool

	// exiting is set the moment runLoop decides to terminate (idle-timeout,
	// ctx cancel, or panic). Dispatchers consult this BEFORE relying on the
	// sessionWorkers.Load() result: if exiting=true, the worker will not
	// drain its inbox, so the dispatcher must spawn a fresh worker for the
	// message instead of enqueueing into the dying one. Without this flag,
	// the window between idleTimer.C firing and the deferred Delete from
	// sessionWorkers running was a silent-message-drop race (pass-2
	// silent-failure-hunter Finding N1).
	exiting atomic.Bool

	// admissionRelease is called when the worker exits to release its slot
	// in the AdmissionController. Set at construction time by the dispatcher.
	admissionRelease func()

	// parent is the owning AgentLoop. Worker calls parent.processMessage and
	// parent.buildContinuationTarget / parent.Continue to run turns.
	// Always non-nil — newSessionWorker panics if parent is nil.
	parent *AgentLoop
}

// newSessionWorker constructs a session worker but does NOT start its goroutine.
// Callers must call go w.runLoop() immediately after spawning.
//
// The worker owns its own context derived from context.Background() — not from
// Run()'s context — so AgentLoop.Close() cancels workers explicitly with a 5 s
// budget rather than relying on run-context propagation.
//
// admissionRelease MUST be the func() returned by AdmissionController.TryAdmit;
// it is called once when the worker's goroutine exits.
func newSessionWorker(scope string, parent *AgentLoop, admissionRelease func()) *sessionWorker {
	if parent == nil {
		panic("newSessionWorker: nil parent")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &sessionWorker{
		scope:            scope,
		inbox:            make(chan bus.InboundMessage, workerInboxCap),
		ctx:              ctx,
		cancel:           cancel,
		done:             make(chan struct{}),
		parent:           parent,
		admissionRelease: admissionRelease,
	}
}

// enqueue delivers a message to the worker. When the worker is currently
// inside processTurn, the message is pushed into the in-turn steering queue
// (so the agent auto-continues with the late-append text after the current
// LLM call returns). Otherwise the message lands in the inbox to start the
// next turn. If the inbox is full the message is dropped and the user receives
// a capacity reply.
func (w *sessionWorker) enqueue(msg bus.InboundMessage) {
	if w.inTurn.Load() {
		// Active turn for this scope — route through the in-turn steering
		// queue so processTurn's post-turn drain (pendingSteeringCountForScope
		// → al.Continue) picks it up as a continuation. Falls back to the
		// inbox path if the steering enqueue rejects (queue full, no active
		// turn state, etc.) so the message is never silently lost.
		if err := w.parent.enqueueSteeringFromMessage(msg); err == nil {
			return
		} else {
			logger.DebugCF("agent.worker", "Steering enqueue rejected — falling back to inbox",
				map[string]any{"scope": w.scope, "error": err.Error()})
		}
	}
	select {
	case w.inbox <- msg:
	default:
		logger.WarnCF("agent.worker", "Session worker inbox full — dropping message",
			map[string]any{
				"scope":   w.scope,
				"channel": msg.Channel,
				"chat_id": msg.ChatID,
			})
		// Notify the user so the drop is not silent.
		rejectCtx, rejectCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer rejectCancel()
		if pubErr := w.parent.bus.PublishOutbound(rejectCtx, bus.OutboundMessage{
			Channel: msg.Channel,
			ChatID:  msg.ChatID,
			Content: "Your message could not be queued — the agent is busy. Please resend in a few seconds.",
		}); pubErr != nil {
			logger.WarnCF("agent.worker", "Failed to send inbox-full reply",
				map[string]any{"channel": msg.Channel, "error": pubErr.Error()})
		}
	}
}

// runLoop is the worker goroutine. It reads messages from inbox and processes
// them one at a time by delegating to the parent AgentLoop's existing turn
// execution path. It self-exits after workerIdleTimeout with no activity and
// removes itself from the parent's sessionWorkers map.
func (w *sessionWorker) runLoop() {
	defer close(w.done)
	defer w.parent.sessionWorkers.Delete(w.scope)
	defer w.admissionRelease()

	defer func() {
		if r := recover(); r != nil {
			logger.ErrorCF("agent.worker", "Panic in session worker runLoop — worker exiting",
				map[string]any{
					"panic": r,
					"scope": w.scope,
					"stack": string(debug.Stack()),
				})
		}
	}()

	idleTimer := time.NewTimer(workerIdleTimeout)
	defer idleTimer.Stop()

	ctx := w.ctx

	for {
		select {
		case <-ctx.Done():
			w.exiting.Store(true)
			return
		case <-idleTimer.C:
			w.exiting.Store(true)
			logger.DebugCF("agent.worker", "Session worker idle-timeout — exiting",
				map[string]any{"scope": w.scope})
			return

		case msg, ok := <-w.inbox:
			if !ok {
				w.exiting.Store(true)
				return
			}

			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(workerIdleTimeout)

			w.processTurn(ctx, msg)
		}
	}
}

// processTurn executes one full turn for msg, then drains any steering
// messages that accumulated during the turn. This is the logic that was
// previously inlined inside Run()'s anonymous func() closure, extracted here
// so each session worker has its own independent execution path.
func (w *sessionWorker) processTurn(ctx context.Context, msg bus.InboundMessage) {
	al := w.parent

	// Mark the worker as inside a turn so concurrent enqueue() calls for the
	// same scope route to the in-turn steering queue rather than the inbox.
	// Cleared even on panic so the worker doesn't get stuck in-turn.
	w.inTurn.Store(true)
	defer w.inTurn.Store(false)

	defer func() {
		// Snapshot the channel manager under its read lock (N-A race fix: the field
		// is written by restartServices→SetChannelManager on a different goroutine).
		if cm := al.getChannelManager(); cm != nil {
			cm.InvokeTypingStop(msg.Channel, msg.ChatID)
		}
	}()

	// FR-004: deferred response guard — identical to the original Run() logic.
	var finalResponse string
	var activeAgent *AgentInstance
	published := false
	publishChannel := msg.Channel
	publishChatID := msg.ChatID

	defer func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorCF("agent.worker", "Panic in deferred response guard",
					map[string]any{"panic": r, "scope": w.scope})
			}
		}()
		if finalResponse != "" && !published {
			al.publishResponseIfNeeded(ctx, activeAgent, publishChannel, publishChatID, finalResponse)
			published = true
		}
	}()

	// C8 (chat-stream-hang): guarantee a terminal frame on EVERY exit path —
	// success, provider error, ctx cancel, AND panic-recover. processMessage →
	// runAgentLoop → runTurn can panic (e.g. a provider/tool nil-deref that
	// escapes the inner recovers). When it does, finalResponse is still "" and
	// no streamer may have been set as ts.lastStreamer, so finalizeStreamer
	// emits no "done" frame either — leaving the SPA stuck "thinking" forever.
	// This recover synthesizes an error response and publishes it so the client
	// receives a terminal frame (publishResponseIfNeeded → webchatChannel.Send →
	// token + done).
	//
	// Registered AFTER the response guard above so that — defers being LIFO —
	// THIS recover runs FIRST during panic unwinding, catches the panic, sets
	// finalResponse, and marks published=true. The response guard then runs and
	// is a no-op (published already true). It re-panics so runLoop's recover
	// still logs the worker-level event and the worker exits cleanly.
	defer func() {
		if r := recover(); r != nil {
			logger.ErrorCF("agent.worker", "Panic in processTurn — emitting terminal error frame",
				map[string]any{"panic": r, "scope": w.scope, "stack": string(debug.Stack())})
			if finalResponse == "" {
				finalResponse = "Error processing message: the agent turn failed unexpectedly. Please try again."
			}
			if finalResponse != "" && !published {
				// ctx may be canceled during panic unwinding; use a fresh,
				// short-lived context so the terminal frame still reaches the client.
				termCtx, termCancel := context.WithTimeout(context.Background(), 5*time.Second)
				al.publishResponseIfNeeded(termCtx, activeAgent, publishChannel, publishChatID, finalResponse)
				termCancel()
				published = true
			}
			// Re-panic so runLoop's recover logs the worker-level event and the
			// worker exits cleanly (a fresh worker is spawned on the next message).
			panic(r)
		}
	}()

	response, agent, err := al.processMessage(ctx, msg)
	activeAgent = agent
	if err != nil {
		response = fmt.Sprintf("Error processing message: %v", err)
	}
	finalResponse = response

	target, targetErr := al.buildContinuationTarget(msg)
	if targetErr != nil {
		logger.WarnCF("agent.worker", "Failed to build steering continuation target",
			map[string]any{
				"scope":   w.scope,
				"channel": msg.Channel,
				"error":   targetErr.Error(),
			})
		return
	}
	if target == nil {
		if finalResponse != "" {
			al.publishResponseIfNeeded(ctx, activeAgent, msg.Channel, msg.ChatID, finalResponse)
			published = true
		}
		return
	}

	// Update the defer's publish target to the resolved continuation target.
	publishChannel = target.Channel
	publishChatID = target.ChatID

	// Drain steering messages that were queued during this turn (user typed
	// a follow-up while the agent was still in its tool loop).
	for al.pendingSteeringCountForScope(target.SessionKey) > 0 {
		logger.InfoCF("agent.worker", "Continuing queued steering after turn end",
			map[string]any{
				"scope":       w.scope,
				"channel":     target.Channel,
				"chat_id":     target.ChatID,
				"session_key": target.SessionKey,
				"queue_depth": al.pendingSteeringCountForScope(target.SessionKey),
			})

		continued, continueErr := al.Continue(ctx, target.SessionKey, target.Channel, target.ChatID)
		if continueErr != nil {
			logger.WarnCF("agent.worker", "Failed to continue queued steering",
				map[string]any{
					"scope":   w.scope,
					"channel": target.Channel,
					"chat_id": target.ChatID,
					"error":   continueErr.Error(),
				})
			return
		}
		if continued == "" {
			return
		}
		finalResponse = continued
		published = false
	}

	if finalResponse != "" {
		al.publishResponseIfNeeded(ctx, activeAgent, target.Channel, target.ChatID, finalResponse)
		published = true
	}
}
