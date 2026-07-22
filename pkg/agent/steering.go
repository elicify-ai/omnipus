package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/routing"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// SteeringMode controls how queued steering messages are dequeued.
type SteeringMode string

const (
	// SteeringOneAtATime dequeues only the first queued message per poll.
	SteeringOneAtATime SteeringMode = "one-at-a-time"
	// SteeringAll drains the entire queue in a single poll.
	SteeringAll SteeringMode = "all"
	// MaxQueueSize number of possible messages in the Steering Queue
	MaxQueueSize = 10
	// manualSteeringScope is the legacy fallback queue used when no active
	// turn/session scope is available.
	manualSteeringScope = "__manual__"
)

// parseSteeringMode normalizes a config string into a SteeringMode.
func parseSteeringMode(s string) SteeringMode {
	switch s {
	case "all":
		return SteeringAll
	default:
		return SteeringOneAtATime
	}
}

// steeringQueue is a thread-safe queue of user messages that can be injected
// into a running agent loop to interrupt it between tool calls.
type steeringQueue struct {
	mu     sync.Mutex
	queues map[string][]providers.Message
	mode   SteeringMode
}

func newSteeringQueue(mode SteeringMode) *steeringQueue {
	return &steeringQueue{
		queues: make(map[string][]providers.Message),
		mode:   mode,
	}
}

func normalizeSteeringScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return manualSteeringScope
	}
	return scope
}

// push enqueues a steering message in the legacy fallback scope.
func (sq *steeringQueue) push(msg providers.Message) error {
	return sq.pushScope(manualSteeringScope, msg)
}

// pushScope enqueues a steering message for the provided scope.
func (sq *steeringQueue) pushScope(scope string, msg providers.Message) error {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	scope = normalizeSteeringScope(scope)
	queue := sq.queues[scope]
	if len(queue) >= MaxQueueSize {
		return fmt.Errorf("steering queue is full")
	}
	sq.queues[scope] = append(queue, msg)
	return nil
}

// dequeue removes and returns pending steering messages from the legacy
// fallback scope according to the configured mode.
func (sq *steeringQueue) dequeue() []providers.Message {
	return sq.dequeueScope(manualSteeringScope)
}

// dequeueScope removes and returns pending steering messages for the provided
// scope according to the configured mode.
func (sq *steeringQueue) dequeueScope(scope string) []providers.Message {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	return sq.dequeueLocked(normalizeSteeringScope(scope))
}

// dequeueScopeWithFallback drains the scoped queue first and falls back to the
// legacy manual scope for backwards compatibility.
func (sq *steeringQueue) dequeueScopeWithFallback(scope string) []providers.Message {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	scope = strings.TrimSpace(scope)
	if scope != "" {
		if msgs := sq.dequeueLocked(scope); len(msgs) > 0 {
			return msgs
		}
	}

	return sq.dequeueLocked(manualSteeringScope)
}

func (sq *steeringQueue) dequeueLocked(scope string) []providers.Message {
	queue := sq.queues[scope]
	if len(queue) == 0 {
		return nil
	}

	switch sq.mode {
	case SteeringAll:
		msgs := append([]providers.Message(nil), queue...)
		delete(sq.queues, scope)
		return msgs
	default:
		msg := queue[0]
		queue[0] = providers.Message{} // Clear reference for GC
		queue = queue[1:]
		if len(queue) == 0 {
			delete(sq.queues, scope)
		} else {
			sq.queues[scope] = queue
		}
		return []providers.Message{msg}
	}
}

// len returns the number of queued messages across all scopes.
func (sq *steeringQueue) len() int {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	total := 0
	for _, queue := range sq.queues {
		total += len(queue)
	}
	return total
}

// lenScope returns the number of queued messages for a specific scope.
func (sq *steeringQueue) lenScope(scope string) int {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return len(sq.queues[normalizeSteeringScope(scope)])
}

// setMode updates the steering mode.
func (sq *steeringQueue) setMode(mode SteeringMode) {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	sq.mode = mode
}

// getMode returns the current steering mode.
func (sq *steeringQueue) getMode() SteeringMode {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return sq.mode
}

// Steer enqueues a user message to be injected into the currently running
// agent loop. The message will be picked up after the current tool finishes
// executing, causing any remaining tool calls in the batch to be skipped.
func (al *AgentLoop) Steer(msg providers.Message) error {
	scope := ""
	agentID := ""
	if ts := al.getAnyActiveTurnState(); ts != nil {
		scope = ts.sessionKey
		agentID = ts.agentID
	}
	return al.enqueueSteeringMessage(scope, agentID, msg)
}

// enqueueSteeringFromMessage redirects an inbound bus message into the
// steering queue for the scope that owns the currently running turn for
// this message's chat. Used by sessionWorker.enqueue when a same-scope
// message arrives while the worker is mid-turn, so the agent auto-continues
// with the late-append text rather than starting a fresh turn after.
//
// Returns an error if the routing target cannot be resolved, no turn is
// active for the scope, or the steering queue is full (caller falls back
// to inbox dispatch in that case).
func (al *AgentLoop) enqueueSteeringFromMessage(msg bus.InboundMessage) error {
	route, ag, err := al.resolveMessageRoute(msg)
	if err != nil || ag == nil {
		return fmt.Errorf("enqueueSteeringFromMessage: route resolution failed: %w", err)
	}
	// The steering queue uses route.SessionKey ("agent:<id>:<sid>") — the same
	// key that runTurn registered the active turn under in activeTurnStates.
	pmsg := providers.Message{
		Role:    "user",
		Content: msg.Content,
		Media:   append([]string(nil), msg.Media...),
	}
	return al.enqueueSteeringMessage(route.SessionKey, ag.ID, pmsg)
}

// EnqueueSteeringMessage is the exported wrapper around enqueueSteeringMessage
// for callers outside this package (pkg/tools/delegate.go's `steer` action —
// tools cannot reach the unexported method directly, mirroring the existing
// SubTurnSpawner-interface pattern used to avoid a tools<->agent import
// cycle). Behavior is byte-for-byte identical to the internal method.
func (al *AgentLoop) EnqueueSteeringMessage(scope, agentID string, msg providers.Message) error {
	return al.enqueueSteeringMessage(scope, agentID, msg)
}

func (al *AgentLoop) enqueueSteeringMessage(scope, agentID string, msg providers.Message) error {
	if al.steering == nil {
		return fmt.Errorf("steering queue is not initialized")
	}

	if err := al.steering.pushScope(scope, msg); err != nil {
		logger.WarnCF("agent", "Failed to enqueue steering message", map[string]any{
			"error": err.Error(),
			"role":  msg.Role,
			"scope": normalizeSteeringScope(scope),
		})
		return err
	}

	queueDepth := al.steering.lenScope(scope)
	logger.DebugCF("agent", "Steering message enqueued", map[string]any{
		"role":        msg.Role,
		"content_len": len(msg.Content),
		"media_count": len(msg.Media),
		"queue_len":   queueDepth,
		"scope":       normalizeSteeringScope(scope),
	})

	meta := EventMeta{
		Source:    "Steer",
		TracePath: "turn.interrupt.received",
	}
	if ts := al.getAnyActiveTurnState(); ts != nil {
		meta = ts.eventMeta("Steer", "turn.interrupt.received")
	} else {
		if strings.TrimSpace(agentID) != "" {
			meta.AgentID = agentID
		}
		normalizedScope := normalizeSteeringScope(scope)
		if normalizedScope != manualSteeringScope {
			meta.SessionKey = normalizedScope
		}
		if meta.AgentID == "" {
			if registry := al.GetRegistry(); registry != nil {
				if agent := registry.GetDefaultAgent(); agent != nil {
					meta.AgentID = agent.ID
				}
			}
		}
	}

	al.emitEvent(
		EventKindInterruptReceived,
		meta,
		InterruptReceivedPayload{
			Kind:       InterruptKindSteering,
			Role:       msg.Role,
			ContentLen: len(msg.Content),
			QueueDepth: queueDepth,
		},
	)

	return nil
}

// SteeringMode returns the current steering mode.
func (al *AgentLoop) SteeringMode() SteeringMode {
	if al.steering == nil {
		return SteeringOneAtATime
	}
	return al.steering.getMode()
}

// SetSteeringMode updates the steering mode.
func (al *AgentLoop) SetSteeringMode(mode SteeringMode) {
	if al.steering == nil {
		return
	}
	al.steering.setMode(mode)
}

// dequeueSteeringMessages is the internal method called by the agent loop
// to poll for steering messages in the legacy fallback scope.
func (al *AgentLoop) dequeueSteeringMessages() []providers.Message {
	if al.steering == nil {
		return nil
	}
	return al.steering.dequeue()
}

func (al *AgentLoop) dequeueSteeringMessagesForScope(scope string) []providers.Message {
	if al.steering == nil {
		return nil
	}
	return al.steering.dequeueScope(scope)
}

func (al *AgentLoop) dequeueSteeringMessagesForScopeWithFallback(scope string) []providers.Message {
	if al.steering == nil {
		return nil
	}
	return al.steering.dequeueScopeWithFallback(scope)
}

func (al *AgentLoop) pendingSteeringCountForScope(scope string) int {
	if al.steering == nil {
		return 0
	}
	return al.steering.lenScope(scope)
}

func (al *AgentLoop) continueWithSteeringMessages(
	ctx context.Context,
	agent *AgentInstance,
	sessionKey, channel, chatID string,
	steeringMsgs []providers.Message,
) (string, error) {
	return al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:              sessionKey,
		Channel:                 channel,
		ChatID:                  chatID,
		DefaultResponse:         defaultResponse,
		EnableSummary:           true,
		SendResponse:            false,
		InitialSteeringMessages: steeringMsgs,
		SkipInitialSteeringPoll: true,
	})
}

func (al *AgentLoop) agentForSession(sessionKey string) *AgentInstance {
	registry := al.GetRegistry()
	if registry == nil {
		return nil
	}

	if parsed := routing.ParseAgentSessionKey(sessionKey); parsed != nil {
		if agent, ok := registry.GetAgent(parsed.AgentID); ok {
			return agent
		}
	}

	return registry.GetDefaultAgent()
}

// Continue resumes an idle agent by dequeuing any pending steering messages
// and running them through the agent loop. This is used when the agent's last
// message was from the assistant (i.e., it has stopped processing) and the
// user has since enqueued steering messages.
//
// If no steering messages are pending, it returns an empty string.
func (al *AgentLoop) Continue(ctx context.Context, sessionKey, channel, chatID string) (string, error) {
	if active := al.GetActiveTurn(); active != nil {
		return "", fmt.Errorf("turn %s is still active", active.TurnID)
	}
	if err := al.ensureHooksInitialized(ctx); err != nil {
		return "", err
	}
	if err := al.ensureMCPInitialized(ctx); err != nil {
		return "", err
	}

	steeringMsgs := al.dequeueSteeringMessagesForScopeWithFallback(sessionKey)
	if len(steeringMsgs) == 0 {
		return "", nil
	}

	agent := al.agentForSession(sessionKey)
	if agent == nil {
		return "", fmt.Errorf("no agent available for session %q", sessionKey)
	}

	if tool, ok := agent.Tools.Get("send_message"); ok {
		if resetter, ok := tool.(interface{ ResetSentInRound() }); ok {
			resetter.ResetSentInRound()
		}
	}

	return al.continueWithSteeringMessages(ctx, agent, sessionKey, channel, chatID, steeringMsgs)
}

func (al *AgentLoop) InterruptGraceful(hint string) error {
	ts := al.getAnyActiveTurnState()
	if ts == nil {
		return fmt.Errorf("no active turn")
	}
	if !ts.requestGracefulInterrupt(hint) {
		return fmt.Errorf("turn %s cannot accept graceful interrupt", ts.turnID)
	}

	al.emitEvent(
		EventKindInterruptReceived,
		ts.eventMeta("InterruptGraceful", "turn.interrupt.received"),
		InterruptReceivedPayload{
			Kind:    InterruptKindGraceful,
			HintLen: len(hint),
		},
	)

	return nil
}

// collectDescendantTurnIDs walks activeTurnStates and returns the turn IDs of
// every turnState whose transcriptSessionID matches sessionID. This is the
// canonical session-match predicate shared by InterruptSession and
// RequestCancel so both callers produce an identical descendants list.
//
// The returned slice is freshly allocated; modifying it does not affect the
// sync.Map. Returns nil (not an error) when no matching turns are found.
func (al *AgentLoop) collectDescendantTurnIDs(sessionID string) []string {
	var ids []string
	al.activeTurnStates.Range(func(_, value any) bool {
		ts := value.(*turnState)
		if ts.transcriptSessionID == sessionID {
			ids = append(ids, ts.turnID)
		}
		return true
	})
	return ids
}

// InterruptSession gracefully cancels the parent turn AND every sub-turn sharing
// the given sessionID (transcriptSessionID match). FR-6, FR-10, FR-12a, FR-15.
//
// The cascade walks activeTurnStates and, for each matching turnState, spawns a
// goroutine that calls requestGracefulInterrupt AND providerCancel in parallel so
// the in-flight LLM HTTP request is aborted immediately (FR-12a) rather than
// waiting for the stream to drain naturally within the 3s graceful window.
//
// Returns the list of turn IDs that received the cancel signal (parent + sub-turns).
// The cancel handler includes this in the turn_canceled audit/transcript entry.
// Returns an error only if sessionID is empty. A session with no active turns is
// not an error — cancel handlers treat it as a no-op (was_fired=false).
func (al *AgentLoop) InterruptSession(sessionID, hint string) (descendants []string, err error) {
	if sessionID == "" {
		return nil, fmt.Errorf("empty session_id")
	}

	// Collect all matching turn states before spawning goroutines so we hold the
	// sync.Map range lock for as short a time as possible.
	var matches []*turnState
	al.activeTurnStates.Range(func(_, value any) bool {
		ts := value.(*turnState)
		if ts.transcriptSessionID == sessionID {
			matches = append(matches, ts)
			descendants = append(descendants, ts.turnID)
		}
		return true // continue walking — cascade to ALL matches (parent + sub-turns)
	})

	if len(matches) == 0 {
		return nil, nil // no active turn — caller emits turn_cancel_attempt{was_fired:false}
	}

	var wg sync.WaitGroup
	for _, ts := range matches {
		// capture loop variable
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.ErrorCF("agent", "Panic in InterruptSession goroutine",
						map[string]any{"panic": r, "turn_id": ts.turnID})
				}
			}()
			// FR-12a: call providerCancel first so the in-flight HTTP stream is
			// aborted immediately, before the graceful-interrupt flag is polled.
			ts.mu.Lock()
			pc := ts.providerCancel
			ts.mu.Unlock()
			if pc != nil {
				pc()
			}
			if ts.requestGracefulInterrupt(hint) {
				al.emitEvent(
					EventKindInterruptReceived,
					ts.eventMeta("InterruptSession", "turn.interrupt.received"),
					InterruptReceivedPayload{
						Kind:    InterruptKindGraceful,
						HintLen: len(hint),
					},
				)
			}
		}()
	}
	wg.Wait()
	return descendants, nil
}

// InterruptSessionHard escalates a previously-graceful cancel to a hard abort for
// every turn matching sessionID. Called at t=3s after InterruptSession per FR-11.
// See InterruptHard for the legacy single-turn path; this function is session-scoped.
//
// Returns the list of turn IDs that received the hard-abort signal.
func (al *AgentLoop) InterruptSessionHard(sessionID, hint string) (descendants []string, err error) {
	if sessionID == "" {
		return nil, fmt.Errorf("empty session_id")
	}

	var matches []*turnState
	al.activeTurnStates.Range(func(_, value any) bool {
		ts := value.(*turnState)
		if ts.transcriptSessionID == sessionID {
			matches = append(matches, ts)
			descendants = append(descendants, ts.turnID)
		}
		return true
	})

	if len(matches) == 0 {
		return nil, nil
	}

	var wg sync.WaitGroup
	for _, ts := range matches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.ErrorCF("agent", "Panic in InterruptSessionHard goroutine",
						map[string]any{"panic": r, "turn_id": ts.turnID})
				}
			}()
			// requestHardAbort sets hardAbort and fires providerCancel+turnCancel
			// atomically (see turn.go:requestHardAbort). The else branch executes
			// only when hardAbort was already true — meaning a concurrent caller
			// already flipped the flag and fired providerCancel. We re-fire it here
			// defensively in case its turnCancel pointer was reset between the two
			// calls (e.g. a new turn started on the same turnState slot).
			if !ts.requestHardAbort() {
				// Concurrent caller already set hardAbort — re-fire providerCancel
				// in case the pointer was replaced since that call.
				ts.mu.Lock()
				pc := ts.providerCancel
				ts.mu.Unlock()
				if pc != nil {
					pc()
				}
			}
			al.emitEvent(
				EventKindInterruptReceived,
				ts.eventMeta("InterruptSessionHard", "turn.interrupt.received"),
				InterruptReceivedPayload{
					Kind: InterruptKindHard,
				},
			)
		}()
	}
	wg.Wait()
	return descendants, nil
}

// sessionTurnsStillAlive returns every turnState matching sessionID
// (transcriptSessionID equality — the same predicate InterruptSession/
// InterruptSessionHard/collectDescendantTurnIDs use) that has NOT yet
// finished (turnState.IsAlive()).
//
// Root cause this closes (delegation-cancel bug, UAT persona "Alex"):
// RequestCancel's PHASE B/C escalation timers (pkg/agent/cancel.go) used to
// gate hard-abort escalation on `activeTurn.IsAlive()` alone, where
// activeTurn is the SINGLE hook resolved once via GetActiveTurnHookForSession
// — which always prefers the ROOT turn when one exists (see that method's
// doc comment). A background (async) delegate sub-turn shares the parent's
// transcriptSessionID but is a SEPARATE turnState; when the root/parent turn
// finishes gracefully within the 3s escalation window (routine — the
// graceful providerCancel() an interrupt fires usually unwinds the root's
// own in-flight LLM call almost immediately), `activeTurn.IsAlive()` goes
// false and the OLD code skipped InterruptSessionHard for the WHOLE
// session — never reaching the still-running child at all. The child's own
// graceful nudge (also delivered by InterruptSession's cascade) only aborts
// its CURRENT in-flight LLM call; because the retry loop only treats a
// canceled call as terminal when THIS turn's own hardAbortRequested() is
// true (pkg/agent/loop.go, the `errors.Is(err, context.Canceled)` checks),
// an un-hard-aborted child simply retries with a fresh, uncanceled context
// and keeps running — invisibly, for as long as its own task takes (minutes,
// for a multi-step delegate) — until it resurfaces, sometimes concurrently
// with a later, unrelated delegate call on the same session.
//
// Callers use this to decide whether ANY turn in the session cascade —
// not just the one originally resolved — is still alive and therefore
// still needs the hard-abort escalation.
func (al *AgentLoop) sessionTurnsStillAlive(sessionID string) []*turnState {
	if sessionID == "" {
		return nil
	}
	var alive []*turnState
	al.activeTurnStates.Range(func(_, value any) bool {
		ts := value.(*turnState)
		if ts.transcriptSessionID == sessionID && ts.IsAlive() {
			alive = append(alive, ts)
		}
		return true
	})
	return alive
}

// hasLiveCriticalDelegate reports whether any NON-ROOT turnState matching
// sessionID (depth>0 / parentTurnID != "" — i.e. a delegate sub-turn, never
// the root itself) that is marked Critical (SubTurnConfig.Critical,
// subturn.go) is currently alive (turnState.IsAlive()). Async delegation
// (`delegate async=true`) unconditionally sets Critical:true (see
// pkg/tools/delegate.go's executeAsync), so this single predicate covers
// both "Critical" and "background/async" as one and the same property —
// ADR-045 uses all three names for what is, mechanically, the identical
// turnState.critical flag.
//
// Mirrors sessionTurnsStillAlive's scan shape but additionally classifies
// root vs. descendant and filters to Critical-only: a live NON-Critical
// descendant's lifetime is bound to its parent's own goroutine (synchronous
// delegation blocks the parent's own runTurn until the child returns, and
// the child's activeTurnStates entry is removed via spawnSubTurn's own
// `defer al.activeTurnStates.Delete(childID)` before spawnSubTurn itself
// ever returns) — so it can only be observed alive here while the root
// itself is ALSO still alive, a case the orphan watchdog's fire predicate
// already requires (a live root) before this check is even consulted.
//
// Used exclusively by the orphan-foreground-turn watchdog (ADR-045,
// pkg/agent/orphan_watch.go) to decide whether reaping the session's root
// turn via RequestCancel would risk cascading into wanted background work —
// RequestCancel's own PHASE B/C escalation is session-wide by construction
// and cannot be scoped to "the root only" from the outside, so the watchdog
// defers reaping entirely rather than reusing it while a Critical delegate
// survives.
func (al *AgentLoop) hasLiveCriticalDelegate(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	found := false
	al.activeTurnStates.Range(func(_, value any) bool {
		ts := value.(*turnState)
		if ts.transcriptSessionID != sessionID {
			return true
		}
		if ts.depth == 0 || ts.parentTurnID == "" {
			return true // root, not a delegate
		}
		if ts.critical && ts.IsAlive() {
			found = true
			return false
		}
		return true
	})
	return found
}

// IsSubTurnActiveForSpawnCall reports whether a sub-turn spawned by the spawn
// tool call identified by parentSpawnCallID is currently registered as an
// active turn — meaning either not-yet-finished, OR finished but its
// persisted spawn-call record has not yet been corrected with the real
// terminal status (see the "Active therefore covers TWO distinct states"
// paragraph below). Returns false for an empty ID or when no matching
// turnState is found in activeTurnStates.
//
// Root cause this closes (reload/replay fabricated-status bug, live UAT
// re-verification 2026-07): async delegation (DelegateTool.executeAsync)
// persists a placeholder ack — Status="success", DurationMS≈0 — on the
// spawning ToolCall record the instant the tool call itself returns, well
// before the delegate's own sub-turn actually finishes (see
// session.UnifiedStore.UpdateToolCallStatus's doc comment). That placeholder
// is only corrected once the real EventKindSubTurnEnd fires, at the END of
// spawnSubTurn's cleanup defer. A session reload/replay landing in the
// window between the placeholder write and that correction previously read
// the placeholder literally and presented a fabricated "done 0ms" snapshot
// for a delegate that was, in truth, still working — invisible before
// 7dd9e7a5 ("background delegate's final answer lost when parent finishes
// first") because a delegate needing more than one LLM turn used to exit its
// loop early the instant its parent turn ended, closing this window almost
// immediately; Critical:true now lets it run for its full, genuine duration
// (sometimes tens of seconds), making the window routinely observable.
//
// Callers (pkg/gateway/replay.go's isSpanActive, pkg/gateway/websocket.go's
// orphan watchdog) use this to distinguish "genuinely still running" from
// "genuinely finished" before trusting a persisted terminal snapshot or
// synthesizing one of their own.
//
// "Active" therefore covers TWO distinct states, not one: genuinely still
// running (ts.IsAlive()), OR finished but the spawning tool-call's placeholder
// record has not yet been corrected with the real terminal status/duration
// (!ts.subTurnRecordPersisted.Load()). Collapsing those into a single
// "isFinished" check reopens the exact bug this primitive exists to close:
// runTurn's own `defer ts.Finish(false)` (loop.go) flips isFinished true and
// fully returns BEFORE spawnSubTurn's own cleanup defer (subturn.go) even
// begins correcting the placeholder ToolCall record written earlier by
// DelegateTool.executeAsync — a correction that can take up to ~935ms of
// retry backoff plus real I/O. A reload/replay landing in that gap must still
// see "active" so it withholds the fabricated "done 0ms" snapshot rather than
// serving it as genuine. See turnState.subTurnRecordPersisted's doc comment
// (turn.go) for the full mechanics.
func (al *AgentLoop) IsSubTurnActiveForSpawnCall(parentSpawnCallID string) bool {
	if parentSpawnCallID == "" {
		return false
	}
	active := false
	al.activeTurnStates.Range(func(_, value any) bool {
		ts, ok := value.(*turnState)
		if !ok {
			return true
		}
		ts.mu.RLock()
		matches := ts.parentSpawnCallID == parentSpawnCallID
		ts.mu.RUnlock()
		if matches && (ts.IsAlive() || !ts.subTurnRecordPersisted.Load()) {
			active = true
			return false // stop — found a live or persistence-pending match
		}
		return true
	})
	return active
}

func (al *AgentLoop) InterruptHard() error {
	ts := al.getAnyActiveTurnState()
	if ts == nil {
		return fmt.Errorf("no active turn")
	}
	if !ts.requestHardAbort() {
		return fmt.Errorf("turn %s is already aborting", ts.turnID)
	}

	al.emitEvent(
		EventKindInterruptReceived,
		ts.eventMeta("InterruptHard", "turn.interrupt.received"),
		InterruptReceivedPayload{
			Kind: InterruptKindHard,
		},
	)

	return nil
}

// ====================== SubTurn Result Polling ======================

// dequeuePendingSubTurnResults polls the SubTurn result channel for the given
// session and returns all available results without blocking.
// Returns nil if no active turn state exists for this session.
func (al *AgentLoop) dequeuePendingSubTurnResults(sessionKey string) []*tools.ToolResult {
	tsInterface, ok := al.activeTurnStates.Load(sessionKey)
	if !ok {
		return nil
	}
	ts, ok := tsInterface.(*turnState)
	if !ok {
		return nil
	}

	var results []*tools.ToolResult
	for {
		select {
		case result := <-ts.pendingResults:
			if result != nil {
				results = append(results, result)
			}
		default:
			return results
		}
	}
}

// ====================== Hard Abort ======================

// HardAbort immediately cancels the running agent loop for the given session,
// cascading the cancellation to all child SubTurns. This is a destructive operation
// that terminates execution without waiting for graceful cleanup.
//
// Use this when the user explicitly requests immediate termination (e.g., "stop now", "abort").
// For graceful interruption that allows the agent to finish the current tool and summarize,
// use Steer() instead.
func (al *AgentLoop) HardAbort(sessionKey string) error {
	tsInterface, ok := al.activeTurnStates.Load(sessionKey)
	if !ok {
		return fmt.Errorf("no active turn state found for session %s", sessionKey)
	}

	ts, ok := tsInterface.(*turnState)
	if !ok {
		return fmt.Errorf("invalid turn state type for session %s", sessionKey)
	}

	logger.InfoCF("agent", "Hard abort triggered", map[string]any{
		"session_key":            sessionKey,
		"turn_id":                ts.turnID,
		"depth":                  ts.depth,
		"initial_history_length": ts.initialHistoryLength,
	})

	// IMPORTANT: Trigger cascading cancellation FIRST to stop all child SubTurns
	// from adding more messages to the session. This prevents race conditions
	// where rollback happens while children are still writing.
	// Use isHardAbort=true for hard abort to immediately cancel all children.
	ts.Finish(true)

	// Roll back session history to the state before the turn started.
	// SetHistory is explicitly NOT used here: it would overwrite the JSONL archive
	// and reset Skip=0, permanently deleting any turns that were evicted (skipped)
	// before this turn began (SC-001, CRITICAL 1 path 3).
	// RollbackAppended truncates the appended tail AND restores meta.Skip to its
	// turn-start value so mid-turn evictions (windowTrim calls during this turn)
	// are undone — ensuring GetHistory returns the exact pre-turn live window.
	if ts.session != nil {
		targetLen := ts.initialArchiveLen
		// targetSkip = initialArchiveLen - initialHistoryLength is the Skip cursor
		// at turn start. Guard: when initialArchiveLen == math.MaxInt (ReadArchive
		// failed at turn start, rollback is a no-op), pass 0 — irrelevant because
		// RollbackAppended exits early before touching Skip when target >= Count.
		targetSkip := 0
		if targetLen != math.MaxInt {
			targetSkip = targetLen - ts.initialHistoryLength
			if targetSkip < 0 {
				targetSkip = 0
			}
		}
		ts.session.RollbackAppended(sessionKey, targetLen, targetSkip)

		// M4 mirror: verify the rollback actually took effect. RollbackAppended is
		// fire-and-forget (no error return). Re-read the archive and confirm the
		// length dropped to <= targetLen. Skip verification when targetLen is
		// math.MaxInt (ReadArchive failed at turn start — rollback was a no-op by
		// design, so there is nothing meaningful to verify).
		if targetLen != math.MaxInt {
			if postArchive, readErr := ts.session.ReadArchive(context.Background(), sessionKey); readErr == nil {
				if len(postArchive) > targetLen {
					logger.ErrorCF("agent", "HardAbort: rollback did not shrink archive to target",
						map[string]any{
							"session_key": sessionKey,
							"target":      targetLen,
							"after":       len(postArchive),
						})
					return fmt.Errorf(
						"HardAbort: RollbackAppended did not take effect (archive len %d > target %d)",
						len(postArchive),
						targetLen,
					)
				}
			}
			// If ReadArchive itself fails, we can't verify — fall through.
		}
	}

	return nil
}

// ====================== Follow-Up Injection ======================

// InjectFollowUp enqueues a message to be automatically processed after the current
// turn completes. Unlike Steer(), which interrupts the current execution, InjectFollowUp
// waits for the current turn to finish naturally before processing the message.
//
// This is useful for:
// - Automated workflows that need to chain multiple turns
// - Background tasks that should run after the main task completes
// - Scheduled follow-up actions
//
// The message will be processed via Continue() when the agent becomes idle.
func (al *AgentLoop) InjectFollowUp(msg providers.Message) error {
	// InjectFollowUp uses the same steering queue mechanism as Steer(),
	// but the semantic difference is in when it's called:
	// - Steer() is called during active execution to interrupt
	// - InjectFollowUp() is called when planning future work
	//
	// Both end up in the same queue and are processed by Continue()
	// when the agent is idle.
	return al.Steer(msg)
}

// ====================== API Aliases for Design Document Compatibility ======================

// InjectSteering is an alias for Steer() to match the design document naming.
// It injects a steering message into the currently running agent loop.
func (al *AgentLoop) InjectSteering(msg providers.Message) error {
	return al.Steer(msg)
}

// ====================== ADR-053 S3: Typed SessionMessage Transport ======================
//
// GENERALIZATION, not a new mechanism (BOM discipline, delivery brief
// DoD-11): a parent->child `steer`/`respond` SessionMessage is translated
// into the SAME providers.Message this file has always queued and injected
// into the CHILD's steering-queue scope via the EXISTING
// enqueueSteeringMessage — same MaxQueueSize, same drain-at-tool-boundary
// semantics, same skip-remaining-batch behavior (INV-3). No second queue,
// no second injection path is introduced.
//
// Correlation routing for `respond` (INV-4 — answers a parked `question` by
// correlation_id, out-of-order safe) happens ONE LAYER UP, at the caller
// (pkg/tools/delegate.go's respond action): it validates correlation_id
// against the child's parked SessionLifecycleRecord.NeedsInput before ever
// reaching here, and a native child parked on `question(wait=true)` has
// exactly one open correlation at a time — so by the time
// DeliverSessionMessage runs, "which question is this answering" is already
// resolved; only the answer TEXT needs to reach the child's next turn.

// ErrSessionMessageNotTurnInjectable is returned by DeliverSessionMessage
// for any SessionMessage kind that is not a parent->child turn injection
// (steer/respond). Every other kind — child->parent reporting
// (progress/checkpoint/artifact/blocker/question/decision_request/error/
// handback) and engine/session_to_ui kinds (revision_entry/goal_status) —
// is inbox/UI delivery, not a turn injection, and must be routed to the
// durable inbox (pkg/session.MessageInboxStore) or the bounded typed wake
// (AsyncNotifier.WakeParent, async_notifier.go) instead. Distinguishable via
// errors.Is so a bus-consumer wiring (another wave) can branch on it rather
// than treating it as a hard failure.
var ErrSessionMessageNotTurnInjectable = errors.New("agent: session message kind is not a turn injection")

// sessionMessageEnvelope is the minimal shape every SessionMessage variant
// flattens inline (ADR-034 precedent) — used here only to read
// SessionId/Text for the two turn-injectable kinds without a full
// per-variant switch.
type sessionMessageTextEnvelope struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

// DeliverSessionMessage is the single entry point that turns a typed
// ADR-053 SessionMessage into the appropriate EXISTING delivery mechanism.
// For `kind: steer` and `kind: respond` (both parent->child), it enqueues
// the message's Text as a providers.Message into the CHILD's steering-queue
// scope (childSessionKey — the same "agent:<id>:<sid>" scope key runTurn
// registers the active turn under, mirroring enqueueSteeringFromMessage's
// own scope resolution) via the existing enqueueSteeringMessage, so it
// lands at the child's next tool boundary exactly like a chat steer does
// today. Any other kind returns ErrSessionMessageNotTurnInjectable — it is
// the caller's job to route those to the inbox/wake mechanisms instead.
func (al *AgentLoop) DeliverSessionMessage(_ context.Context, childSessionKey, childAgentID string, msg generated.SessionMessage) error {
	kind, err := msg.Discriminator()
	if err != nil {
		return fmt.Errorf("agent: session message: malformed envelope: %w", err)
	}

	switch kind {
	case "steer", "respond":
		raw, merr := msg.MarshalJSON()
		if merr != nil {
			return fmt.Errorf("agent: session message: marshal: %w", merr)
		}
		var env sessionMessageTextEnvelope
		if uerr := json.Unmarshal(raw, &env); uerr != nil {
			return fmt.Errorf("agent: session message: kind %q: %w", kind, uerr)
		}
		if strings.TrimSpace(env.Text) == "" {
			return fmt.Errorf("agent: session message: kind %q: empty text", kind)
		}
		return al.enqueueSteeringMessage(childSessionKey, childAgentID, providers.Message{
			Role:    "user",
			Content: env.Text,
		})
	default:
		return fmt.Errorf("%w: kind %q", ErrSessionMessageNotTurnInjectable, kind)
	}
}
