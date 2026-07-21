package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/dapicom-ai/omnipus/pkg/logger"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/routing"
	"github.com/dapicom-ai/omnipus/pkg/tools"
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

	if tool, ok := agent.Tools.Get("message"); ok {
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

// InterruptByChannelChat gracefully cancels the active root turn (depth==0)
// whose channel and chatID match the supplied values, then cascades to all
// sub-turns that share the same transcriptSessionID via InterruptSession.
//
// This is the correct cancellation path for Tier B (text-parsing) channels:
// inbound messages from those channels carry no explicit SessionID so a direct
// InterruptSession call would match nothing. Sub-turns inherit their parent's
// transcriptSessionID but NOT channel/chatID (they are created with empty
// values), so matching by channel+chatID alone misses them. The two-step
// strategy — find root by channel+chatID, then cascade by sessionID — covers
// both parent and all sub-turns.
//
// Returns nil whether or not any matching turn was found — "no active turn" is
// a valid no-op. Returns a non-nil error only when channel or chatID is empty.
func (al *AgentLoop) InterruptByChannelChat(channel, chatID, hint string) error {
	if channel == "" || chatID == "" {
		return fmt.Errorf("InterruptByChannelChat: channel and chatID must be non-empty")
	}

	// Step 1: find the root turn (depth==0) matching channel+chatID and extract
	// its transcriptSessionID. We stop at the first match because a given
	// channel+chatID pair can have at most one active root turn at a time.
	var sid string
	al.activeTurnStates.Range(func(_, value any) bool {
		ts := value.(*turnState)
		ts.mu.RLock()
		ch := ts.channel
		cid := ts.chatID
		depth := ts.depth
		ts.mu.RUnlock()
		if ch == channel && cid == chatID && depth == 0 {
			sid = ts.transcriptSessionID
			return false // stop walking — found the root
		}
		return true
	})

	if sid == "" {
		// No active root turn for this channel+chatID — valid no-op.
		return nil
	}

	// Step 2: cascade via InterruptSession which covers parent + all sub-turns
	// that share the same transcriptSessionID.
	_, err := al.InterruptSession(sid, hint)
	return err
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
	if ts.session != nil {
		history := ts.session.GetHistory(sessionKey)
		if ts.initialHistoryLength < len(history) {
			ts.session.SetHistory(sessionKey, history[:ts.initialHistoryLength])
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
