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
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

const (
	// continueDrainMaxRetries bounds processTurn's drain-loop retry of a
	// failing al.Continue call before giving up and abandoning the queued
	// steering messages (design note "Recommended design"). The mixed
	// failure causes (a transient init hiccup vs. a permanent nil-agent
	// cause vs. an active-turn guard that should never legitimately trip
	// post-fix) are deliberately NOT classified by type — a uniform bounded
	// retry is simpler to review and just as safe, since the retry budget
	// itself, not the cause, bounds worst-case cost.
	continueDrainMaxRetries = 3
)

// continueDrainBackoff is the delay schedule between successive
// continueDrainMaxRetries attempts of al.Continue inside processTurn's drain
// loop — short and escalating, sub-2-second total worst case. This blocks
// only this session's own worker goroutine and touches no shared resource or
// lock other sessions depend on.
var continueDrainBackoff = []time.Duration{100 * time.Millisecond, 300 * time.Millisecond, 700 * time.Millisecond}

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

	// inbox receives messages dispatched by Run(). It is a SMALL channel
	// (workerInboxBuffer): the one waiting-item bound, MaxQueueSize, is counted
	// on enqueue over inbox plus overflow rather than preallocated here, so an
	// idle worker costs a few slots, not MaxQueueSize of them. Waiting more than
	// MaxQueueSize → drop with WARN and notify user (see enqueue).
	inbox chan bus.InboundMessage

	// inboxMu guards overflow and makes the bound check atomic with the add.
	inboxMu sync.Mutex
	// overflow holds the waiting messages that did not fit in inbox, oldest
	// first. Invariant: overflow is non-empty only while inbox is non-empty
	// (every receive is followed by refillInbox), so FIFO order is inbox then
	// overflow.
	overflow []bus.InboundMessage

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
	// Every transition that decides where a message goes happens under
	// steerMu (see trySteerIntoLiveTurn / closeSteeringWhenDrained).
	inTurn atomic.Bool

	// steerMu makes "steer this message into the live turn" and "the live
	// turn has drained its steering queue for the last time" mutually
	// exclusive. Without it a message arriving after processTurn's final
	// drain check but before inTurn cleared was pushed into a steering queue
	// no one would ever drain again — the message was silently never run.
	steerMu sync.Mutex

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

// workerInboxBuffer is the inbox channel's buffer: only the hot path, not a
// capacity. The capacity is MaxQueueSize, counted in enqueue (see pushInbox).
const workerInboxBuffer = 8

// waitingInboxCount is the number of messages waiting to start a turn.
func (w *sessionWorker) waitingInboxCount() int {
	w.inboxMu.Lock()
	defer w.inboxMu.Unlock()
	return len(w.inbox) + len(w.overflow)
}

// pushInbox adds msg behind everything already waiting, reporting false when
// MaxQueueSize messages are already waiting.
func (w *sessionWorker) pushInbox(msg bus.InboundMessage) bool {
	w.inboxMu.Lock()
	defer w.inboxMu.Unlock()
	if len(w.inbox)+len(w.overflow) >= MaxQueueSize {
		return false
	}
	if len(w.overflow) == 0 {
		select {
		case w.inbox <- msg:
			return true
		default:
		}
	}
	w.overflow = append(w.overflow, msg)
	return true
}

// refillInbox moves waiting overflow messages into the channel after a receive
// freed room, keeping FIFO order.
func (w *sessionWorker) refillInbox() {
	w.inboxMu.Lock()
	defer w.inboxMu.Unlock()
	for len(w.overflow) > 0 {
		select {
		case w.inbox <- w.overflow[0]:
			w.overflow[0] = bus.InboundMessage{}
			w.overflow = w.overflow[1:]
		default:
			return
		}
	}
	w.overflow = nil
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
		scope: scope,
		// FR-009 / DEL-03: ONE waiting-item bound (MaxQueueSize, 200), enforced
		// in enqueue. The channel itself stays small: preallocating the bound
		// gave every session worker 200 message slots and pushed
		// TestCompactionBoundsMemory from ~3 MB to ~10 MB. The deeper
		// unification of this inbox into the steering FIFO is a follow-up.
		inbox:            make(chan bus.InboundMessage, workerInboxBuffer),
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
	if w.trySteerIntoLiveTurn(msg) {
		return true
	}
	if w.pushInbox(msg) {
		return true
	}
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

// trySteerIntoLiveTurn routes msg into the live turn's steering queue when
// this worker is inside a user turn that has not yet finished draining that
// queue, so processTurn's post-turn drain (closeSteeringWhenDrained →
// al.Continue) picks it up as a continuation. Returns false — the caller
// queues msg in the inbox as the next turn — when there is no such turn, for
// a system message, during a #505 system turn, or when the steering enqueue
// rejects (queue full, classify or revive failure), so the message is never
// silently lost.
//
// The inTurn check and the enqueue run under steerMu, the same lock
// closeSteeringWhenDrained holds while it confirms the queue is empty and
// clears inTurn: a message is either in the queue before that final check
// (and drained) or sees inTurn=false (and goes to the inbox) — never in
// between.
func (w *sessionWorker) trySteerIntoLiveTurn(msg bus.InboundMessage) bool {
	if msg.Channel == "system" {
		return false
	}
	w.steerMu.Lock()
	defer w.steerMu.Unlock()
	if !w.inTurn.Load() || w.systemTurn.Load() {
		return false
	}
	if err := w.parent.enqueueSteeringFromMessage(msg); err != nil {
		// A steering-rejected error here means the message was NOT accepted
		// by the steering path (classify failure, revive failure). It is
		// about to run again from the inbox — the fallback that made
		// silent-failure-hunter #1 into two runs — so the rejection reason
		// must be visible at the default log level, not Debug.
		logger.WarnCF("agent.worker", "Steering enqueue rejected — falling back to inbox",
			map[string]any{"scope": w.scope, "error": err.Error()})
		return false
	}
	return true
}

// closeSteeringWhenDrained is processTurn's final drain check: it reports
// true, and clears inTurn in the same critical section, only when no
// steering message is pending for sessionKey. From then on enqueue sends
// every message for this scope to the inbox, where it runs as the next turn.
// Returns false while messages are pending — the caller continues the turn
// with them and checks again.
func (w *sessionWorker) closeSteeringWhenDrained(sessionKey string) bool {
	w.steerMu.Lock()
	defer w.steerMu.Unlock()
	if w.parent.pendingSteeringCountForScope(sessionKey) > 0 {
		return false
	}
	w.inTurn.Store(false)
	return true
}

// dispatchSessionWorker delivers msg to the sessionWorker that owns scope,
// spawning one under the admission controller when none exists yet. Returns
// false when admission refused (at capacity) or when a system message found
// the worker's inbox full (see enqueue) — the caller decides the fallback.
// Extracted from Run()'s dispatch loop so #505's system-message
// dispatch (dispatchSystemMessageToSessionWorker) uses the exact same
// load-or-spawn mechanics as every other inbound message.
func (al *AgentLoop) dispatchSessionWorker(scope string, msg bus.InboundMessage) bool {
	// A person typing into a helper's own pane steers that existing helper
	// (Steering commands C1): the helper's own execution consumes it, never
	// a second ordinary turn on the helper's session.
	if msg.Channel != "system" && al.deliverHumanHelperInput(msg) {
		return true
	}
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

			w.refillInbox()
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
	ctx, executionEntry := ordinaryExecutionContext(ctx)
	defer func() {
		if err := al.finishExecutionDisposition(executionEntry.disposition); err != nil {
			al.reportOrdinarySettlementFailure(msg, err)
		}
	}()

	// Mark the worker as inside a turn so concurrent enqueue() calls for the
	// same scope route to the in-turn steering queue rather than the inbox.
	// Cleared even on panic so the worker doesn't get stuck in-turn.
	w.steerMu.Lock()
	w.inTurn.Store(true)
	w.steerMu.Unlock()
	defer func() {
		w.steerMu.Lock()
		w.inTurn.Store(false)
		w.steerMu.Unlock()
	}()
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
	publishSessionID := msg.SessionID

	defer func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorCF("agent.worker", "Panic in deferred response guard",
					map[string]any{"panic": r, "scope": w.scope})
			}
		}()
		if finalResponse != "" && !published {
			al.publishResponseIfNeeded(ctx, activeAgent, publishChannel, publishChatID, finalResponse, publishSessionID)
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
				al.publishResponseIfNeeded(termCtx, activeAgent, publishChannel, publishChatID, finalResponse, publishSessionID)
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
		// err stays in worker logging for operator triage — the error-level
		// log below is that record.
		//
		// userVisibleTurnError, not TranslateLLMError(nil, err.Error()):
		// passing the error VALUE keeps the sentinels intact, so a turn
		// refused for a known reason (agent on no workspace) says so instead
		// of falling to the "we can't tell why" copy, and a curatedTurnError
		// in the chain (the in-flight stop-fence refusals, hook/budget
		// aborts) is published as written.
		logger.ErrorCF("agent.worker", "Turn failed — raw error for operator triage",
			map[string]any{"session_id": msg.SessionID, "error": err.Error()})
		response = userVisibleTurnError(err)
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
				al.publishResponseIfNeeded(ctx, activeAgent, msg.Channel, msg.ChatID, finalResponse, msg.SessionID)
				published = true
			}
			return
		}
		// Exit (c) (design note addendum): a genuine (non-ErrNoContinuationTarget)
		// routing failure. buildContinuationTarget's only fallible step is
		// resolveMessageRoute(msg) — the steering queue's real key
		// (route.SessionKey) can only be produced by a SUCCESSFUL call to the
		// exact function that just failed, and w.scope is NOT a safe
		// substitute (it can carry a ":"+msg.SessionID suffix the queue's
		// real key never has, silently checking/touching the wrong bucket).
		// So this branch deliberately does NOT check or touch the steering
		// queue at all — only a generic, visible notice is published. w.inTurn
		// needs no special handling here: this is before the drain loop
		// starts, and the function-level defer above already clears it
		// unconditionally on every return.
		logger.ErrorCF("agent.worker", "Failed to build steering continuation target — any queued follow-up for this session cannot be safely identified and was left untouched",
			map[string]any{
				"scope":   w.scope, // operator triage only — NOT used to touch the queue
				"channel": msg.Channel,
				"chat_id": msg.ChatID,
				"error":   targetErr.Error(),
			})
		notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 3*time.Second)
		if pubErr := al.bus.PublishOutbound(notifyCtx, bus.OutboundMessage{
			Channel: msg.Channel,
			ChatID:  msg.ChatID,
			Content: "A problem occurred while checking for further pending instructions in this conversation. If you sent a follow-up message, please resend it.",
		}); pubErr != nil {
			logger.WarnCF("agent.worker", "Failed to publish continuation-target-failure notice",
				map[string]any{"scope": w.scope, "error": pubErr.Error()})
		}
		notifyCancel()
		return
	}
	if target == nil {
		if finalResponse != "" {
			al.publishResponseIfNeeded(ctx, activeAgent, msg.Channel, msg.ChatID, finalResponse, msg.SessionID)
			published = true
		}
		return
	}

	// Update the defer's publish target to the resolved continuation target.
	publishChannel = target.Channel
	publishChatID = target.ChatID
	publishSessionID = target.SessionID

	// Drain steering messages that were queued during this turn (user typed
	// a follow-up while the agent was still in its tool loop). The loop ends
	// only through closeSteeringWhenDrained, which clears inTurn atomically
	// with the empty-queue check, so a message arriving after this point goes
	// to the inbox instead of a queue nobody drains again.
	for !w.closeSteeringWhenDrained(target.SessionKey) {
		logger.InfoCF("agent.worker", "Continuing queued steering after turn end",
			map[string]any{
				"scope":       w.scope,
				"channel":     target.Channel,
				"chat_id":     target.ChatID,
				"session_key": target.SessionKey,
				"queue_depth": al.pendingSteeringCountForScope(target.SessionKey),
			})

		// Exit (a) (design note "Recommended design"): bounded retry, then
		// fail loud. See continueDrainRetry's own doc comment for why a
		// uniform bounded retry is simpler to review and just as safe here.
		continued, attemptsMade, continueErr := w.continueDrainRetry(ctx, target)
		if continueErr != nil {
			w.abandonQueuedSteering(ctx, target, continueErr, attemptsMade)
			return
		}
		if continued == "" {
			return
		}
		finalResponse = continued
		published = false
	}

	if finalResponse != "" {
		al.publishResponseIfNeeded(ctx, activeAgent, target.Channel, target.ChatID, finalResponse, target.SessionID)
		published = true
	}
}

// continueDrainRetry is processTurn's drain-loop bounded-retry step (design
// note "Recommended design", exit (a)): repeatedly calls al.Continue for one
// drained steering item until it succeeds, exhausts continueDrainMaxRetries,
// or hits a post-dequeue failure. Post Bug-1/Bug-2 fixes, a same-session
// GetActiveTurnBySession collision here is an invariant violation, not an
// expected transient state — the realistic failure causes are
// ensureHooksInitialized, ensureMCPInitialized (possibly transient) or
// agentForSession returning nil (deterministic/permanent). Not classifying by
// cause: a uniform bounded retry is simpler to review and just as safe.
//
// Gate finding, CRITICAL: a POST-dequeue failure (errContinuePostDequeueFailure)
// means Continue already ran runTurn's ordinary, tool-capable turn pipeline
// (the same one every other turn uses) against the dequeued message(s)
// before it errored — not a cheap pre-flight guard. Retrying that here would
// re-invoke the same tool-capable pipeline from scratch against identical
// restored content, risking re-firing a tool call that already succeeded
// inside the failed attempt — this returns immediately instead, so the
// caller's abandonQueuedSteering can dequeue-and-report it straight away.
// The four PRE-dequeue causes (active-turn guard, hooks/MCP init,
// agentForSession==nil) never call runTurn at all, so they have no such risk
// and correctly keep the bounded-retry-with-backoff behavior below.
func (w *sessionWorker) continueDrainRetry(ctx context.Context, target *continuationTarget) (continued string, attemptsMade int, continueErr error) {
	al := w.parent
	return retrySteeringContinuation(ctx, func() (string, error) {
		return al.Continue(ctx, target.SessionKey, target.Channel, target.ChatID, target.WorkspaceID)
	}, func(err error) bool {
		return errors.Is(err, errContinuePostDequeueFailure)
	})
}

// retrySteeringContinuation is the shared bounded-retry policy for post-turn
// drains. stopOn identifies errors that must not be replayed because the
// attempted continuation either crossed the destructive dequeue boundary or
// became ineligible to run. The backoff shape deliberately remains identical
// for ordinary session workers and steered children.
func retrySteeringContinuation(
	ctx context.Context,
	run func() (string, error),
	stopOn func(error) bool,
) (continued string, attemptsMade int, continueErr error) {
retryLoop:
	for attempt := 0; attempt < continueDrainMaxRetries; attempt++ {
		attemptsMade = attempt + 1
		continued, continueErr = run()
		if continueErr == nil {
			break
		}
		if stopOn != nil && stopOn(continueErr) {
			break
		}
		if attempt < len(continueDrainBackoff) {
			// Gate finding, MEDIUM: select on ctx.Done() so a Close()
			// cancellation mid-retry does not force the drain loop to sit
			// through the whole remaining backoff schedule before noticing —
			// matches AgentLoop.Close()'s own 5s worker-cancellation budget
			// instead of fighting it.
			select {
			case <-time.After(continueDrainBackoff[attempt]):
			case <-ctx.Done():
				break retryLoop
			}
		}
	}
	return continued, attemptsMade, continueErr
}

// abandonQueuedSteering is processTurn's drain-loop failure path (design note
// addendum exit (a)): called once al.Continue has exhausted its bounded
// retry budget (continueDrainMaxRetries) and a queued steering message still
// cannot be delivered. It is the first and only consumption attempt against
// the steering queue for this failure — Continue's own reorder fix (Bug 2)
// guarantees a failed Continue call never touches the queue, so nothing was
// consumed during the retries themselves.
//
// Three things happen, in order: (1) the queue is dequeued and discarded —
// "dequeued-and-reported", never left silently stuck; (2) w.inTurn is
// explicitly cleared under w.steerMu — load-bearing, not cosmetic: without
// this the NEXT message for this session would hit trySteerIntoLiveTurn,
// see inTurn still true, and re-enqueue into the very queue just abandoned,
// which nothing will ever drain again; (3) a distinct, user-visible failure
// notice is published — never blended into finalResponse, which carries the
// turn's own separate answer.
//
// The dequeue-then-publish section is wrapped in its own recover so that even
// a panic in this narrow window degrades to "still in the queue, recoverable
// later" rather than "vaporized" — mirroring consumeDequeuedSteering's own
// wake-failure branch (pkg/agent/steering.go), which restores an unconsumed
// suffix via steeringQueue.prependItemsScope on a partial failure.
func (w *sessionWorker) abandonQueuedSteering(ctx context.Context, target *continuationTarget, lastErr error, attempts int) {
	al := w.parent
	queueDepthBefore := al.pendingSteeringCountForScope(target.SessionKey)

	var abandonedItems []steeringQueueItem
	var abandonedScope string
	defer func() {
		if r := recover(); r != nil {
			if len(abandonedItems) > 0 {
				al.steering.prependItemsScope(abandonedScope, abandonedItems)
			}
			logger.ErrorCF("agent.worker", "Panic while abandoning queued steering — restored to queue",
				map[string]any{
					"scope":       w.scope,
					"session_key": target.SessionKey,
					"panic":       r,
					"stack":       string(debug.Stack()),
				})
			panic(r)
		}
	}()

	// Gate finding, Important: dequeue via the item-preserving primitive, not
	// dequeueSteeringMessagesForScopeWithFallback's flattened
	// (msgs, correlationIDs) — reconstructing a steeringQueueItem from those
	// alone drops the wake field a wake-bearing item (steer_audience.go's
	// EnqueueSteeringWake) legitimately carries, so a panic in this narrow
	// window would restore it minus the pointer writeSteeringConsumedMarker's
	// consume-ack bookkeeping depends on.
	abandonedScope, abandonedItems, _, _ = al.dequeueSteeringItemsForScopeWithFallback(target.SessionKey)

	// Load-bearing (see doc comment above): the next message for this
	// session must go to the inbox as a fresh turn, not be steered into a
	// queue nobody will ever drain again.
	w.steerMu.Lock()
	w.inTurn.Store(false)
	w.steerMu.Unlock()

	logger.ErrorCF("agent.worker", "Persistent Continue failure — abandoning queued steering and notifying user",
		map[string]any{
			"scope":       w.scope,
			"session_key": target.SessionKey,
			"queue_depth": queueDepthBefore,
			"attempts":    attempts,
			"error":       lastErr.Error(),
		})

	// Gate finding, HIGH: this publish gets its OWN short-timeout context —
	// matching the 3 other publish call sites in this file (dispatchSessionWorker's
	// capacity-rejection notice, processTurn's exit-(c) notice) — instead of
	// the ambient worker ctx, which AgentLoop.Close() cancels only on its own
	// schedule (never per-call) and could otherwise block this indefinitely
	// on a stalled/full outbound bus.
	//
	// Same mechanism the drain loop's own success path uses two lines below
	// in processTurn (publishResponseIfNeeded, not a bespoke bus call) — but a
	// SECOND, distinct message, never blended into finalResponse, since the
	// turn's own real answer and "your follow-up may not have gone through"
	// are two different things the user should be able to tell apart.
	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer notifyCancel()
	al.publishResponseIfNeeded(notifyCtx, nil, target.Channel, target.ChatID,
		"Your follow-up message could not be processed and was not delivered — please resend it.", target.SessionID)
}
