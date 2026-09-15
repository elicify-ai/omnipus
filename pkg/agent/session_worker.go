// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
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

	// systemTurn reports whether the turn currently executing was triggered
	// by a #505 system message (an async-origin completion). While true,
	// enqueue does NOT steer same-scope user messages into that turn: a
	// system-triggered processTurn has no continuation target of its own
	// (buildContinuationTarget short-circuits Channel=="system"), so a
	// steered message could strand in the steering queue with no drain. It
	// queues in the inbox instead and runs as its own serialized turn after.
	systemTurn atomic.Bool

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
//
// Exception — Channel=="system" (#505): a system message (an async delegate/
// background completion reconstructed into its own turn by
// processSystemMessage) is NEVER steered into a live turn. Steering would
// merge the completion's content into the running turn's history as an
// ordinary user message and drop processSystemMessage's own attribution and
// transcript binding; instead it queues in the inbox and runs as its own
// serialized turn once the live one finishes. This is also what guarantees
// the no-deadlock property #505's dispatch relies on: a live turn waiting on
// the very delegation whose completion this message carries never waits on
// this queue — the inbox is buffered and never blocks the enqueueing
// dispatcher.
//
// Returns false in exactly one case: a system message that found the inbox
// full. It is NOT dropped and no busy reply is published (that reply would go
// to the internal "system" channel nobody reads, so the background result
// would simply vanish); the caller falls back to Run()'s unserialized dispatch
// instead, which never loses a result. Every other outcome returns true,
// including a user message dropped on a full inbox (that drop is announced to
// the user, exactly as before).
func (w *sessionWorker) enqueue(msg bus.InboundMessage) bool {
	if w.inTurn.Load() && msg.Channel != "system" && !w.systemTurn.Load() {
		// Active turn for this scope — route through the in-turn steering
		// queue so processTurn's post-turn drain (pendingSteeringCountForScope
		// → al.Continue) picks it up as a continuation. Falls back to the
		// inbox path if the steering enqueue rejects (queue full, no active
		// turn state, etc.) so the message is never silently lost.
		if err := w.parent.enqueueSteeringFromMessage(msg); err == nil {
			return true
		} else {
			logger.DebugCF("agent.worker", "Steering enqueue rejected — falling back to inbox",
				map[string]any{"scope": w.scope, "error": err.Error()})
		}
	}
	select {
	case w.inbox <- msg:
		return true
	default:
		if msg.Channel == "system" {
			logger.WarnCF("agent.worker", "Session worker inbox full — system message not queued; falling back to unserialized dispatch",
				map[string]any{
					"scope":      w.scope,
					"chat_id":    msg.ChatID,
					"session_id": msg.AsyncTranscriptSessionID,
				})
			return false
		}
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
		return true
	}
}

// dispatchSessionWorker delivers msg to the sessionWorker that owns scope,
// spawning one under the admission controller when none exists yet. Returns
// false when admission refused (at capacity) or when a system message found
// the worker's inbox full (see enqueue) — the caller decides the fallback.
// Extracted from Run()'s dispatch loop so #505's system-message
// dispatch (dispatchSystemMessageToSessionWorker) uses the exact same
// load-or-spawn mechanics as every other inbound message.
func (al *AgentLoop) dispatchSessionWorker(scope string, msg bus.InboundMessage) bool {
	// If a worker already exists for this scope AND is not in the
	// middle of exiting, enqueue into it. The exiting check closes the
	// silent-drop race (pass-2 silent-failure-hunter N1) where
	// the dispatcher Load'd a worker whose idleTimer had already
	// fired but whose deferred sessionWorkers.Delete had not yet
	// run — enqueue into the dying worker's inbox would never be
	// drained. When exiting=true we fall through to the spawn path
	// below, which will create a fresh worker.
	if existing, ok := al.sessionWorkers.Load(scope); ok {
		w, ok := existing.(*sessionWorker)
		if !ok {
			logger.ErrorCF("agent", "sessionWorkers: invariant violated — unexpected value type",
				map[string]any{"scope": scope, "got_type": fmt.Sprintf("%T", existing)})
			// Fall through to spawn a replacement, same as a dying worker.
		} else if !w.exiting.Load() {
			return w.enqueue(msg)
		}
		// Dying worker (or corrupted entry) — fall through to spawn replacement.
	}

	// No worker yet — atomically claim an admission slot for this scope.
	// TryAdmit returns (true, release) when admitted; (false, nil) when at cap.
	// Using TryAdmit rather than a separate ShouldAdmit+OnTurnStart pair
	// closes the TOCTOU window where two concurrent dispatchers both pass
	// the check and overshoot the cap.
	admitted, release := al.admission.TryAdmit(scope)
	if !admitted {
		logger.WarnCF("agent", "At capacity — rejecting new session",
			map[string]any{
				"scope":    scope,
				"active":   al.admission.ActiveScopes(),
				"soft_cap": al.admission.SoftCap(),
				"channel":  msg.Channel,
				"chat_id":  msg.ChatID,
			})
		// Send a user-visible capacity reply. For a system-channel message
		// this publish is inert downstream (the channels manager skips
		// internal channels) — the WARN above is the operator-visible trace.
		rejectCtx, rejectCancel := context.WithTimeout(context.Background(), 3*time.Second)
		if pubErr := al.bus.PublishOutbound(rejectCtx, bus.OutboundMessage{
			Channel: msg.Channel,
			ChatID:  msg.ChatID,
			Content: "I'm at capacity right now — please try again in a few seconds.",
		}); pubErr != nil {
			logger.WarnCF("agent", "Failed to send capacity-rejection reply",
				map[string]any{"channel": msg.Channel, "error": pubErr.Error()})
		}
		rejectCancel()
		return false
	}

	// Spawn a new worker for this scope. The worker holds the admission
	// slot via release() and calls it in its deferred runLoop cleanup.
	w := newSessionWorker(scope, al, release)
	al.sessionWorkers.Store(scope, w)
	go w.runLoop()
	return w.enqueue(msg)
}

// dispatchSystemMessageToSessionWorker routes an async-origin system message
// (#505, AsyncTranscriptSessionID set) through the per-session sessionWorker
// that owns its origin session, so the reconstructed turn is serialized
// against that session's live turns instead of running on a bare unscoped
// goroutine. originChannel is the parsed origin channel of msg.ChatID
// ("channel:chat_id"). Returns false when no dispatch was possible (no live
// worker AND the probe route does not resolve, or admission refused) — the
// caller falls back to the legacy bare-goroutine path.
//
// Worker selection, in order:
//
//  1. A LIVE worker whose scope ends in ":"+sessionID — the one shape
//     guarantee resolveSteeringTarget makes for every SessionID-keyed message
//     (see cancel_prearm.go's identical suffix-matching rationale). This
//     matches whichever agent-shaped key the live turn used (explicit
//     per-message agent, handoff pin, default route).
//  2. Otherwise the scope a user message for the same channel/chat/session
//     would resolve to (resolveSteeringTarget on a probe copy of msg shaped
//     like that traffic), spawning a worker under it — so the session's NEXT
//     user message lands on the same worker. A session whose live routing
//     diverges from the probe (e.g. an agent chosen per-message via metadata
//     while no worker is live) is the residual gap and stays on the bare
//     path, no worse than before #505's fix.
//
// The worker runs the message through processMessage → processSystemMessage
// (the exact function the bare goroutine called), preserving FIX 5d's agent
// attribution, transcript binding and session-scoped history key.
func (al *AgentLoop) dispatchSystemMessageToSessionWorker(msg bus.InboundMessage, originChannel string) bool {
	sid := msg.AsyncTranscriptSessionID

	// 1. Live worker for this session (any agent shape).
	var live *sessionWorker
	al.sessionWorkers.Range(func(key, value any) bool {
		wscope, _ := key.(string)
		if !strings.HasSuffix(wscope, ":"+sid) {
			return true
		}
		if w, ok := value.(*sessionWorker); ok && w != nil && !w.exiting.Load() {
			live = w
			return false
		}
		return true
	})
	if live != nil {
		return live.enqueue(msg)
	}

	// 2. No live worker — resolve the scope this session's own user traffic
	// would use and spawn under it.
	originChatID := msg.ChatID
	if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
		originChatID = msg.ChatID[idx+1:]
	}
	probe := msg
	probe.Channel = originChannel
	probe.ChatID = originChatID
	probe.SessionID = sid
	scope, _, ok := al.resolveSteeringTarget(probe)
	if !ok {
		return false
	}
	return al.dispatchSessionWorker(scope, msg)
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
	// #505: a system-triggered turn additionally suppresses steering (see
	// systemTurn's field comment) — a same-scope user message arriving now
	// queues in the inbox as its own serialized turn.
	w.systemTurn.Store(msg.Channel == "system")
	defer w.systemTurn.Store(false)

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
		// ADR-051 §RD5: never surface raw err text in the assistant-facing
		// reply. Route through the classifier so provider-originated body /
		// status / model identity is replaced with the typed copy. The raw
		// err stays in worker logging for operator triage.
		//
		// TranslateTurnError, not TranslateLLMError(nil, err.Error()): passing
		// the error VALUE keeps the sentinels intact, so a turn refused for a
		// known reason (agent on no workspace) says so instead of falling to
		// the "we can't tell why" copy.
		response = TranslateTurnError(err).Message
	}
	finalResponse = response

	// #505: a system-triggered turn's response was already delivered inside
	// processSystemMessage itself (its processOptions carry SendResponse=true,
	// unlike a user turn's, which deliberately leaves delivery to this
	// worker's response guard). Marking it published AND clearing
	// finalResponse keeps every later publish in this function from sending
	// a second copy — the deferred guard, the no-continuation-target return
	// and the post-drain publish all key on finalResponse — and keeps the
	// error path quiet exactly as the legacy bare-goroutine dispatcher was
	// (it only logged). Only a genuine steering continuation in the drain
	// loop below sets a new finalResponse, which is then delivered normally.
	if msg.Channel == "system" {
		published = true
		finalResponse = ""
	}

	target, targetErr := al.buildContinuationTarget(msg)
	if targetErr != nil && errors.Is(targetErr, ErrNoContinuationTarget) && msg.Channel == "system" && msg.AsyncTranscriptSessionID != "" {
		// #505: a system-triggered turn has no continuation target of its
		// own, but a user message may STILL have been steered into this
		// session's steering queue during the turn (the systemTurn flag in
		// enqueue suppresses that, yet it cannot close the window before
		// processTurn stores it). Derive the drain target from a probe of
		// this session's own user traffic — the same shaping
		// dispatchSystemMessageToSessionWorker uses — and fall through to the
		// normal drain below so nothing strands in the queue. When the probe
		// does not resolve either, targetErr stays ErrNoContinuationTarget
		// and the early return below behaves exactly as before.
		probe := msg
		if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
			probe.Channel = msg.ChatID[:idx]
			probe.ChatID = msg.ChatID[idx+1:]
		} else {
			probe.Channel = "cli"
		}
		probe.SessionID = msg.AsyncTranscriptSessionID
		target, targetErr = al.buildContinuationTarget(probe)
		if targetErr != nil {
			target = nil
		}
	}
	if targetErr != nil {
		if errors.Is(targetErr, ErrNoContinuationTarget) {
			if finalResponse != "" {
				al.publishResponseIfNeeded(ctx, activeAgent, msg.Channel, msg.ChatID, finalResponse)
				published = true
			}
			return
		}
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

		continued, continueErr := al.Continue(ctx, target.SessionKey, target.Channel, target.ChatID, target.WorkspaceID)
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
