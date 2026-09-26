package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/routing"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/google/uuid"
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
	queues map[string][]steeringQueueItem
	mode   SteeringMode
}

type steeringQueueItem struct {
	message providers.Message
	wake    *steeringWake
	// correlationID is issue #870's steering_receipt carrier: set on every
	// item EnqueueSteeringMessage creates (caller-supplied, or server-
	// assigned when blank — see enqueueSteeringMessage), empty for a bare
	// wake with no steer/respond behind it. Travels item->dequeue->
	// pendingMessages->injection unchanged so the receipt stamped at
	// injection (loop_run_turn.go) can name the steer it belongs to.
	correlationID string
}

type steeringWake struct {
	messageID           string
	transcriptSessionID string
	agentID             string
}

func newSteeringQueue(mode SteeringMode) *steeringQueue {
	return &steeringQueue{
		queues: make(map[string][]steeringQueueItem),
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
	return sq.pushItemScope(scope, steeringQueueItem{message: msg})
}

func (sq *steeringQueue) pushItemScope(scope string, item steeringQueueItem) error {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	scope = normalizeSteeringScope(scope)
	queue := sq.queues[scope]
	if len(queue) >= MaxQueueSize {
		return fmt.Errorf("steering queue is full")
	}
	sq.queues[scope] = append(queue, item)
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

	return steeringMessages(sq.dequeueItemsLocked(normalizeSteeringScope(scope)))
}

// dequeueScopeWithFallback was deleted 2026-09-24 as an unreachable
// ADR-091 leftover (golangci unused): grep found no caller anywhere in
// the repo — dequeueScope (above) is the one actually wired in.

func (sq *steeringQueue) dequeueItemsLocked(scope string) []steeringQueueItem {
	queue := sq.queues[scope]
	if len(queue) == 0 {
		return nil
	}

	switch sq.mode {
	case SteeringAll:
		items := append([]steeringQueueItem(nil), queue...)
		delete(sq.queues, scope)
		return items
	default:
		item := queue[0]
		queue[0] = steeringQueueItem{} // Clear reference for GC
		queue = queue[1:]
		if len(queue) == 0 {
			delete(sq.queues, scope)
		} else {
			sq.queues[scope] = queue
		}
		return []steeringQueueItem{item}
	}
}

func (sq *steeringQueue) dequeueItemsScope(scope string) (string, []steeringQueueItem) {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	scope = normalizeSteeringScope(scope)
	return scope, sq.dequeueItemsLocked(scope)
}

func (sq *steeringQueue) dequeueItemsScopeWithFallback(scope string) (string, []steeringQueueItem) {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	scope = strings.TrimSpace(scope)
	if scope != "" {
		if items := sq.dequeueItemsLocked(scope); len(items) > 0 {
			return scope, items
		}
	}
	return manualSteeringScope, sq.dequeueItemsLocked(manualSteeringScope)
}

func (sq *steeringQueue) prependItemsScope(scope string, items []steeringQueueItem) {
	if len(items) == 0 {
		return
	}
	sq.mu.Lock()
	defer sq.mu.Unlock()
	scope = normalizeSteeringScope(scope)
	queue := sq.queues[scope]
	restored := make([]steeringQueueItem, 0, len(items)+len(queue))
	restored = append(restored, items...)
	restored = append(restored, queue...)
	sq.queues[scope] = restored
}

func steeringMessages(items []steeringQueueItem) []providers.Message {
	if len(items) == 0 {
		return nil
	}
	msgs := make([]providers.Message, 0, len(items))
	for _, item := range items {
		msgs = append(msgs, item.message)
	}
	return msgs
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
	_, err := al.enqueueSteeringMessage(scope, agentID, msg, "")
	return err
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
	// [Finding 1, ADR-091 fix lane 2 — Q17/D8] The human writing into an
	// open child (D5/Q9): sessionWorker.enqueue only reaches this path while
	// w.inTurn is still true, but a Stop's durable marker lands before its
	// cooperative grace window elapses — a message arriving in that window
	// would otherwise be enqueued into a steering queue whose consumer is
	// about to disappear for good, the false-success Finding 1 closes for the
	// delegate tool's own steer/respond. Only a newer instruction revives it.
	handled, herr := al.reviveInactiveInbound(route, msg)
	if herr != nil {
		return herr
	}
	if handled {
		return nil
	}
	// The steering queue uses route.SessionKey ("agent:<id>:<sid>") — the same
	// key that runTurn registered the active turn under in activeTurnStates.
	pmsg := providers.Message{
		Role:    "user",
		Content: msg.Content,
		Media:   append([]string(nil), msg.Media...),
	}
	_, err = al.enqueueSteeringMessage(route.SessionKey, ag.ID, pmsg, "")
	return err
}

// reviveInactiveInbound applies ADR-093 D4 when msg's session is terminal or
// stopped for its current generation. handled is true when the message was
// taken onto a new generation and must not also be enqueued. A blank id, a
// missing store, an unreadable record, or a live record is not handled: the
// caller enqueues as before. When ReviveStoppedSession declines, handled is
// false and the caller enqueues too.
//
// An ordinary root is revived and then run as a fresh inbound turn (MAJ-003:
// never a steered redispatch, never the steered instruction write). Damaged
// and legacy rows (nil classify error, not an ordinary root) keep the
// pre-ADR-093 revive-and-redispatch; a classify ERROR — including unreadable
// metadata — fails the enqueue and does NOT revive.
func (al *AgentLoop) reviveInactiveInbound(route routing.ResolvedRoute, msg bus.InboundMessage) (bool, error) {
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		return false, nil
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return false, nil
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		if errors.Is(err, session.ErrLifecycleNotFound) {
			return false, nil
		}
		logger.ErrorCF("agent", "adr093: could not read the lifecycle record while routing an inbound message",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return false, fmt.Errorf("enqueueSteeringFromMessage: load %q: %w", sessionID, err)
	}
	if !(rec.Terminal() || rec.Stopped()) {
		return false, nil
	}
	classifier := NewSteerRecordClassifier(lifecycle, al.ResolveSessionStore(sessionID))
	class, cerr := classifier.Classify(context.Background(), sessionID)
	if cerr != nil {
		return false, fmt.Errorf("enqueueSteeringFromMessage: classify %q: %w", sessionID, cerr)
	}
	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: msg.GatewayUserID}
	if class == steer.ClassOrdinaryRoot {
		// ADR-093 D4 + MIN-001: the revival itself is synchronous (the message
		// must not be queued while the record is still terminal), but the TURN
		// is not run inline — enqueueSteeringFromMessage is called from Run's
		// dispatch loop, and an inline processMessage would block the pump for
		// a whole turn (gate review F1) and return the turn's error as an
		// "enqueue rejected" signal that session_worker's fallback answers by
		// queuing the SAME message again (silent-failure-hunter #1: two runs,
		// one per turn). runRevivedOrdinaryTurn owns the message from here:
		// exactly one run, published like every other inbound turn (CC-1),
		// under the Run-scoped context (CC-2), failures error-level.
		if rerr := al.reviveRecordForHumanTurn(al.inboundRunContext(), sessionID, by); rerr != nil {
			return false, fmt.Errorf("enqueueSteeringFromMessage: revive ordinary root %q: %w", sessionID, rerr)
		}
		// The handoff wait inside runRevivedOrdinaryTurn looks the replaced
		// turn up in activeTurnStates under the key runTurn registers it
		// under — resolveScopeKey's output, the same expression processMessage
		// evaluates for THIS message (loop.go). The bare lifecycle id is a
		// different string ("agent:<id>:session:<sid>" vs "<sid>"), and a
		// lookup with it always misses, so the wait never ran and the resumed
		// turn overlapped the turn it replaces while that turn was still
		// appending its final history writes (rev2 review, gate CC-2's
		// overlap half).
		al.runRevivedOrdinaryTurn(msg, resolveScopeKey(route, msg.SessionKey))
		return true, nil
	}
	revived, rerr := al.ReviveStoppedSession(context.Background(), sessionID, by, msg.Content)
	if rerr != nil {
		return false, fmt.Errorf("enqueueSteeringFromMessage: revive stopped session %q: %w", sessionID, rerr)
	}
	return revived, nil
}

// ReviveStoppedSession implements Finding 1's fix (ADR-091 fix lane 2 —
// founder decision Q17/D8): "a Stop survives a restart; only a newer
// instruction revives the session, as a new generation." Before this,
// SteerCanceller.Revive had zero production callers — a session durably
// stopped at its current generation (a queued child the cascade stamped, or
// a parked child caught by a Stop) could never run again, and the delegate
// tool's own steer/respond enqueued into a steering queue no live turn would
// ever drain, silently orphaning the message.
//
// Returns (false, nil) when the record is neither terminal nor durably
// stopped at its current generation (ADR-093 D4) — the caller's ordinary path
// applies instead. A TERMINAL record (a finished child) takes this same
// revive path — ADR-093 D4 keeps a terminal child resumable until
// housekeeping deletes it; SteerCanceller.Revive accepts both states.
// instruction, when non-blank, is appended to the session's durable history
// BEFORE dispatch, exactly like follow_up's own appendFollowUpInstruction
// (pkg/tools/delegate_followup.go), so the reconstructed turn actually sees
// it.
func (al *AgentLoop) ReviveStoppedSession(ctx context.Context, sessionID string, by steer.Principal, instruction string) (bool, error) {
	if al == nil {
		return false, fmt.Errorf("steer: revive %q: no AgentLoop wired", sessionID)
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return false, session.ErrLifecycleNotFound
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return false, err
	}
	// Neither terminal nor durably stopped for this generation: nothing to
	// revive — the caller's ordinary path applies. (A terminal child takes
	// the revive path below, not this decline branch.)
	if !rec.Terminal() && !rec.Stopped() {
		return false, nil
	}
	// [Defect 4, ADR-091 fix lane RX-DELIVERY, HIGH] The instruction lands
	// BEFORE the generation is minted, and a failure refuses the revive
	// outright. Both halves matter:
	//   - Ordering: nothing in the lifecycle record is touched until the new
	//     instruction is durable, so a refusal cannot strand a record at a
	//     freshly minted generation in `running` with no turn behind it.
	//   - Refusal: the append used to be fire-and-forget, so a revival whose
	//     new instruction never landed still dispatched — and
	//     reconstructSteeredTurn (wake == nil) then rebuilt the turn from the
	//     last `user` TRANSCRIPT entry, i.e. the ORIGINAL pre-Stop
	//     instruction. The user typed "stop, do X instead" and the session
	//     confidently answered the OLD question, then reported that answer
	//     upward as a real result. Failing the revive is strictly better than
	//     running the wrong instruction.
	if trimmed := strings.TrimSpace(instruction); trimmed != "" {
		if aerr := al.appendSteeredInstruction(sessionID, rec.AgentID, trimmed); aerr != nil {
			return false, fmt.Errorf("steer: revive %q: %w", sessionID, aerr)
		}
	}
	newGeneration, rerr := al.steerCanceller().Revive(ctx, sessionID, by)
	if rerr != nil {
		// Gate SFH#6: record the failed revive so the D2 launch backstop
		// refuses truthfully instead of send-a-new-message — the user's
		// revive attempt itself just failed, so pointing at "send another
		// message" repeats the failure. Sibling site:
		// reviveRecordForHumanTurn.
		al.markRevivalFailure(sessionID, rerr)
		return false, fmt.Errorf("steer: revive %q: %w", sessionID, rerr)
	}
	// ADR-093 MIN-002 (gate SFH#5): the child revive now resets the
	// session-list status to active with the same helper the human-message
	// path uses, so a resumed child no longer stays "interrupted" in the
	// session list while it runs. Failures are error-logged, never
	// success-pretended (gate SFH#4). The failed-revive memory is dropped
	// too: the session IS live again, so a later refusal must not be
	// mislabelled (gate SFH#6).
	al.clearRevivalFailure(sessionID)
	al.resetUnifiedMetaStatusActive(sessionID)
	// al.dispatchSteeredSession IS steer.SessionLauncher.Dispatch's own body
	// (SteerLauncher.Dispatch, steer_launcher.go: "a thin delegate onto
	// AgentLoop.dispatchSteeredSession") — called directly here, exactly as
	// admission.go::drainSteerQueue already does for its own redispatch, so
	// revival works whether or not the optional externally-injected
	// steer.SessionLauncher (SetSteerSessionLauncher, wired post-boot for
	// pkg/tools callers that cannot import pkg/agent) has been set.
	if _, derr := al.dispatchSteeredSession(ctx, sessionID, newGeneration); derr != nil {
		return false, fmt.Errorf("steer: revive %q: dispatch generation %d: %w", sessionID, newGeneration, derr)
	}
	return true, nil
}

// appendSteeredInstruction is ReviveStoppedSession's analogue of
// pkg/tools/delegate_followup.go::appendFollowUpInstruction, for the two
// AgentLoop-native revival paths (a human's message into an open child, and
// the delegate tool's own steer via the steerReviver capability it type-
// asserts al into) rather than the tools-package follow_up flow.
//
// [Defect 4, ADR-091 fix lane RX-DELIVERY] It writes BOTH halves of the pair
// SteerLauncher.Launch already writes for the launch instruction
// (steer_launcher.go: sessions.AddMessage AND
// sessions.AppendTranscriptStrict), and reports whether the instruction
// actually landed. Before this it did neither:
//
//   - The TRANSCRIPT write did not exist. AddMessage writes context.jsonl
//     (the model's history) only, but the turn a revival reconstructs takes
//     its UserMessage from the TRANSCRIPT — steer_reconstruct.go::
//     reconstructSteeredTurn scans transcript.jsonl backwards for the last
//     non-blank `user` entry when wake == nil. So the revived turn re-ran the
//     ORIGINAL pre-Stop instruction even when AddMessage had succeeded.
//   - The ERROR return did not exist. UnifiedStore.AddMessage has no return
//     value at all (it logs internally and tells the caller nothing) and a
//     nil store was a silent no-op, so the caller dispatched regardless.
//
// AppendTranscriptStrict is the strict variant on purpose: it refuses to
// write against a session that does not exist rather than minting an orphan
// directory, and it propagates its transcript.jsonl error — so it is both the
// write the reconstructed turn actually reads AND the one that can report.
// The entry id is fresh per call (a repeat revive is a new instruction, never
// a duplicate of the last one).
func (al *AgentLoop) appendSteeredInstruction(sessionID, agentID, instruction string) error {
	store := al.ResolveSessionStore(sessionID)
	if store == nil {
		return fmt.Errorf("record the new instruction for %q: no session store owns this session", sessionID)
	}
	store.AddMessage(sessionID, "user", instruction)
	if err := store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
		ID:      sessionID + "-instruction-" + uuid.NewString(),
		Role:    "user",
		AgentID: agentID,
		Content: instruction,
	}); err != nil {
		return fmt.Errorf("record the new instruction for %q in the transcript the revived turn reads: %w", sessionID, err)
	}
	return nil
}

// EnqueueSteeringMessage is the exported wrapper used by the delegate tool
// after it has synchronously verified the caller's authority for the target
// session. The queue transports the resulting user message plus a
// correlation id for issue #870's steering_receipt: correlationID is the
// caller-supplied id when non-blank, otherwise EnqueueSteeringMessage mints
// a server-assigned one (SubagentStateFrame.yaml's steering_receipt.
// correlation_id: "supplied, when one was supplied; otherwise a
// server-assigned reference"). The resolved id is always returned so the
// caller (the delegate tool's steer action) can hand it back to whoever
// steered, to match a later receipt to this instruction.
func (al *AgentLoop) EnqueueSteeringMessage(scope, agentID string, msg providers.Message, correlationID string) (string, error) {
	return al.enqueueSteeringMessage(scope, agentID, msg, correlationID)
}

func (al *AgentLoop) enqueueSteeringMessage(scope, agentID string, msg providers.Message, correlationID string) (string, error) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		correlationID = "corr_" + uuid.NewString()
	}
	if err := al.enqueueSteeringItem(scope, agentID, steeringQueueItem{message: msg, correlationID: correlationID}); err != nil {
		return "", err
	}
	return correlationID, nil
}

// EnqueueSteeringWake queues an upward wake into an already-live turn while
// retaining the inbox message identity needed by I-3's consumed marker.
func (al *AgentLoop) EnqueueSteeringWake(scope, agentID, transcriptSessionID, messageID string, msg providers.Message) error {
	if strings.TrimSpace(transcriptSessionID) == "" || strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("steering wake requires transcript session id and message id")
	}
	return al.enqueueSteeringItem(scope, agentID, steeringQueueItem{
		message: msg,
		wake: &steeringWake{
			messageID:           messageID,
			transcriptSessionID: transcriptSessionID,
			agentID:             agentID,
		},
	})
}

func (al *AgentLoop) enqueueSteeringItem(scope, agentID string, item steeringQueueItem) error {
	if al.steering == nil {
		return fmt.Errorf("steering queue is not initialized")
	}

	// Pushed via pushItemScope directly, not pushScope/pushWakeScope: those
	// two rebuild a steeringQueueItem from only message+wake, which would
	// silently drop correlationID (issue #870) on every enqueue.
	err := al.steering.pushItemScope(scope, item)
	if err != nil {
		logger.WarnCF("agent", "Failed to enqueue steering message", map[string]any{
			"error": err.Error(),
			"role":  item.message.Role,
			"scope": normalizeSteeringScope(scope),
		})
		return err
	}

	queueDepth := al.steering.lenScope(scope)
	logger.DebugCF("agent", "Steering message enqueued", map[string]any{
		"role":        item.message.Role,
		"content_len": len(item.message.Content),
		"media_count": len(item.message.Media),
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
			Role:       item.message.Role,
			ContentLen: len(item.message.Content),
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
// to poll for steering messages in the legacy fallback scope. The second
// return value is the parallel correlation-id slice consumeDequeuedSteering
// produces (issue #870) — index i's id belongs to index i's message.
func (al *AgentLoop) dequeueSteeringMessages() ([]providers.Message, []string) {
	if al.steering == nil {
		return nil, nil
	}
	scope, items := al.steering.dequeueItemsScope(manualSteeringScope)
	return al.consumeDequeuedSteering(scope, items)
}

func (al *AgentLoop) dequeueSteeringMessagesForScope(scope string) ([]providers.Message, []string) {
	if al.steering == nil {
		return nil, nil
	}
	actualScope, items := al.steering.dequeueItemsScope(scope)
	return al.consumeDequeuedSteering(actualScope, items)
}

func (al *AgentLoop) dequeueSteeringMessagesForScopeWithFallback(scope string) ([]providers.Message, []string) {
	if al.steering == nil {
		return nil, nil
	}
	actualScope, items := al.steering.dequeueItemsScopeWithFallback(scope)
	return al.consumeDequeuedSteering(actualScope, items)
}

// consumeDequeuedSteering flattens dequeued steeringQueueItems into their
// providers.Message payloads and a parallel slice of correlation ids
// (issue #870): correlationIDs[i] is the id — possibly "" for an item with
// none — that belongs to msgs[i]. Kept as a parallel slice rather than a
// field on providers.Message itself: that struct is the literal wire shape
// of an LLM provider request, never a carrier for receipt bookkeeping.
func (al *AgentLoop) consumeDequeuedSteering(scope string, items []steeringQueueItem) ([]providers.Message, []string) {
	if len(items) == 0 {
		return nil, nil
	}
	msgs := make([]providers.Message, 0, len(items))
	correlationIDs := make([]string, 0, len(items))
	for i, item := range items {
		if item.wake != nil {
			if err := al.writeSteeringConsumedMarker(*item.wake); err != nil {
				al.steering.prependItemsScope(scope, items[i:])
				slog.Error("agent: steering wake not consumed; restored to queue",
					"scope", scope, "message_id", item.wake.messageID, "error", err)
				return msgs, correlationIDs
			}
		}
		msgs = append(msgs, item.message)
		correlationIDs = append(correlationIDs, item.correlationID)
	}
	return msgs, correlationIDs
}

func (al *AgentLoop) writeSteeringConsumedMarker(wake steeringWake) error {
	store := al.ResolveSessionStore(wake.transcriptSessionID)
	if store == nil {
		return fmt.Errorf("no transcript store for session %q", wake.transcriptSessionID)
	}
	return store.AppendTranscriptStrict(wake.transcriptSessionID, session.TranscriptEntry{
		ID:      "consumed-" + wake.messageID,
		Type:    session.EntryTypeSystem,
		Role:    "system",
		Content: "consumed " + wake.messageID,
		AgentID: wake.agentID,
	})
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
	sessionKey, channel, chatID, workspaceID string,
	steeringMsgs []providers.Message,
	steeringCorrelationIDs []string,
) (string, error) {
	return al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:                    sessionKey,
		Channel:                       channel,
		ChatID:                        chatID,
		DefaultResponse:               defaultResponse,
		SendResponse:                  false,
		InitialSteeringMessages:       steeringMsgs,
		InitialSteeringCorrelationIDs: steeringCorrelationIDs,
		SkipInitialSteeringPoll:       true,
		// FIX 1 (re-review): see AgentLoop.resolveWorkspaceIDForContinuation
		// (loop.go) for the resolution this value is sourced from — this
		// function previously left WorkspaceID unset entirely, degrading a
		// steering-continued turn's tool media to the private/global room.
		WorkspaceID: workspaceID,
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
//
// workspaceID (FIX 1 re-review) is the workspace this continuation should run
// inside — the caller (session_worker.go) resolves it once via
// AgentLoop.resolveWorkspaceIDForContinuation from the ORIGINAL triggering
// message and threads it straight through; Continue has no message of its
// own to resolve it from (only the already-collapsed sessionKey/channel/
// chatID), so it cannot recompute this value itself.
func (al *AgentLoop) Continue(ctx context.Context, sessionKey, channel, chatID, workspaceID string) (string, error) {
	if active := al.GetActiveTurn(); active != nil {
		return "", fmt.Errorf("turn %s is still active", active.TurnID)
	}
	if err := al.ensureHooksInitialized(ctx); err != nil {
		return "", err
	}
	if err := al.ensureMCPInitialized(ctx); err != nil {
		return "", err
	}

	steeringMsgs, steeringCorrelationIDs := al.dequeueSteeringMessagesForScopeWithFallback(sessionKey)
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

	return al.continueWithSteeringMessages(ctx, agent, sessionKey, channel, chatID, workspaceID, steeringMsgs, steeringCorrelationIDs)
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
// every turnState whose routingSessionID matches sessionID. This is the
// canonical session-match predicate shared by Interrupt's whole-chat cascade
// and RequestCancel so both callers produce an identical descendants list.
//
// ADR-057 FR-015 (role-B predicate, one of the seven post-W13): rebased from
// transcriptSessionID onto routingSessionID — see turnState.routingSessionID's
// doc comment (turn.go) for the full identity-split rationale, including the
// post-ADR-091 derivation that replaced the direct copy this paragraph
// describes. Pre-D1 (before the deleted spawnSubTurn, pkg/agent/subturn.go,
// ADR-057 U7, overwrote a child's routingSessionID
// onto its root's) this is behaviourally IDENTICAL to the old
// transcriptSessionID match: routingSessionID defaults to a turn's own
// transcriptSessionID at construction (newTurnState, turn.go), and every
// descendant's transcriptSessionID is itself still inherited verbatim
// pre-D1. Post-D1 it remains the correct match: routingSessionID keeps being
// inherited verbatim through the whole subtree while transcriptSessionID
// becomes each delegate's own distinct, store-backed session id.
//
// The returned slice is freshly allocated; modifying it does not affect the
// sync.Map. Returns nil (not an error) when no matching turns are found.
func (al *AgentLoop) collectDescendantTurnIDs(sessionID string) []string {
	var ids []string
	al.activeTurnStates.Range(func(key, value any) bool {
		ts, ok := value.(*turnState)
		if !ok {
			slog.Error("activeTurnStates contains non-*turnState value",
				"session_key", key, "value_type", fmt.Sprintf("%T", value))
			return true
		}
		if string(ts.routingSessionID) == sessionID {
			ids = append(ids, ts.turnID)
		}
		return true
	})
	return ids
}

// InterruptScope names how far a graceful (Interrupt) or hard
// (InterruptSessionHard) interrupt cascades from the turn(s) matching id.
// ADR-057 D8/FR-041: this is the mandatory third argument that collapses the
// four pre-existing entry points (InterruptSession, InterruptSessionHard,
// InterruptBySessionKey, InterruptBySessionKeyHard) into two — one per
// escalation stage — so the compiler forces every caller to name its
// intent. There is no zero-value default that means "pick one for me": the
// only two values are ScopeSubtree and ScopeSelfOnly, and every call site
// must write one of them out.
type InterruptScope int

const (
	// ScopeSubtree reaches the turn(s) matching id AND every turn currently
	// descended from them, walked via the LIVE in-memory parentTurnID chain
	// (collectLiveDescendantTurnStates). Rooted at a CHAT's own session id
	// this reaches the whole delegation tree — byte-identical to the old
	// InterruptSession/InterruptSessionHard cascade, because every member of
	// a chat's tree shares that chat's routingSessionID (resolveInterruptAnchors's
	// Range fallback already finds all of them directly; the descendant walk
	// on top is redundant-but-harmless in that case). Rooted at a DELEGATE's
	// own sessionKey (found via the point-lookup half of
	// resolveInterruptAnchors) it reaches exactly that delegate and its own
	// descendants — never the parent, never a sibling (FR-042, AC-8). This is
	// the NEW capability D8/R-13 add: `delegate action=cancel` moves from
	// "that one turn" to "that child's subtree", closing the
	// grandchild/background-shell leak D4 names.
	ScopeSubtree InterruptScope = iota
	// ScopeSelfOnly reaches EXACTLY the turn(s) resolveInterruptAnchors finds
	// for id — no descendant walk. This is the old
	// InterruptBySessionKey/InterruptBySessionKeyHard direct point-lookup
	// behaviour, byte-identical when id is a delegate's own sessionKey (the
	// only shape any current caller uses it with).
	ScopeSelfOnly
)

// String renders the scope for logs/audit trails.
func (s InterruptScope) String() string {
	switch s {
	case ScopeSubtree:
		return "subtree"
	case ScopeSelfOnly:
		return "self_only"
	default:
		return fmt.Sprintf("InterruptScope(%d)", int(s))
	}
}

// resolveInterruptAnchors finds the LIVE turnState(s) that id addresses,
// trying BOTH mechanisms the four now-collapsed functions used separately —
// exactly the confusion class D8 exists to end, now unified behind one
// resolver instead of left for a caller to pick between:
//
//  1. A direct point Load keyed by sessionKey — the exact mechanism
//     InterruptBySessionKey/InterruptBySessionKeyHard used. Hits when id is
//     a delegate's own sessionKey: turn.go's registerActiveTurn/
//     registerTurnIfAbsent register a delegated child's turnState under its
//     own child session id (SessionKey: childID; pre-ADR-091, the deleted
//     spawnSubTurn did this same registration), which is always unique per
//     delegation regardless of the transcriptSessionID/routingSessionID
//     identity split.
//  2. Only if that misses, a Range matching string(ts.routingSessionID) ==
//     id — the exact mechanism InterruptSession/InterruptSessionHard used
//     (ADR-057 FR-015 role-B predicate, rebased from transcriptSessionID).
//     Hits when id is a CHAT's own session id: a root chat turn is
//     registered under a composite "agent:<id>:<peer>" sessionKey
//     (pkg/routing.BuildAgentPeerSessionKey), never the plain session id, so
//     a point Load with the plain id can never hit it — only the Range,
//     matching on the field every member of that chat's tree shares.
//
// Trying the point Load first is deliberate and load-bearing, not an
// optimization: a delegate's own routingSessionID is NOT its own distinct
// id — it is inherited verbatim from the chat root (turnState.routingSessionID's
// doc comment, turn.go) — so a Range-only lookup keyed on a delegate's own id
// would find zero matches for a delegate address, and if it were instead
// matched against the delegate's shared routingSessionID it would reach the
// whole chat tree, exactly the sibling/cousin-reaching bug #577 fixed. The
// point Load's key space (sessionKey) is the only namespace in which a
// delegate's own address is genuinely unique.
func (al *AgentLoop) resolveInterruptAnchors(id string) []*turnState {
	if id == "" {
		return nil
	}
	if val, ok := al.activeTurnStates.Load(id); ok {
		ts, ok := val.(*turnState)
		if !ok || ts == nil {
			// A Load hit but the value is not a *turnState: activeTurnStates
			// is keyed by sessionKey and every Store puts a *turnState in it
			// (turn.go registerActiveTurn / clearActiveTurnStateEntry), so a
			// non-conforming value means the registry is corrupted. Log the
			// invariant violation at Error and fall through to "no anchor" —
			// there is no live turn to interrupt via this path.
			slog.Error("activeTurnStates contains non-*turnState value",
				"session_key", id, "value_type", fmt.Sprintf("%T", val))
			return nil
		}
		return []*turnState{ts}
	}
	var anchors []*turnState
	al.activeTurnStates.Range(func(key, value any) bool {
		ts, ok := value.(*turnState)
		if !ok {
			slog.Error("activeTurnStates contains non-*turnState value",
				"session_key", key, "value_type", fmt.Sprintf("%T", value))
			return true
		}
		if string(ts.routingSessionID) == id {
			anchors = append(anchors, ts)
		}
		return true
	})
	return anchors
}

// collectLiveDescendantTurnStates returns every turnState currently
// registered in al.activeTurnStates whose parentTurnID chain leads back to
// rootTurnID — i.e. every LIVE turn descended, at any depth, from the turn
// identified by rootTurnID. rootTurnID itself is never included.
//
// KNOWN LIMITATION, stated rather than silently shipped: this walk only
// reaches a descendant through a chain of turnStates that are ALL still
// live. If an intermediate delegate has already finished and been cleared
// from activeTurnStates while ITS OWN child survives (an orphaned
// background/Critical delegate — the exact scenario ADR-045's watchdog,
// pkg/agent/orphan_watch.go, exists to police), that surviving grandchild is
// unreachable through this chain alone. This is a non-issue for
// ScopeSubtree rooted at a CHAT's own session id (the common case, and the
// only shape any current caller uses): resolveInterruptAnchors's Range
// fallback already finds every such orphan directly via its own
// (permanently root-inherited) routingSessionID, with no chain-walk needed —
// this function then only adds members the Range missed, which is nothing
// in that case. It is a real, narrower gap for ScopeSubtree rooted at a
// NON-root delegate whose own subtree contains a mid-chain orphan; no
// FR/BDD/AC in ADR-057's W13 scope exercises that combination, and the
// durable SteeringSessionID walk (D3/D7) — which does not have this gap,
// because it is not in-memory — is reserved by D4 for non-turn resources
// off the escalation path, not for this in-memory turn cascade.
func (al *AgentLoop) collectLiveDescendantTurnStates(rootTurnID string) []*turnState {
	if rootTurnID == "" {
		return nil
	}
	// Snapshot every live turnState once so parentTurnID chains can be walked
	// in memory without repeated sync.Map iteration.
	var all []*turnState
	al.activeTurnStates.Range(func(_, value any) bool {
		if ts, ok := value.(*turnState); ok && ts != nil {
			all = append(all, ts)
		}
		return true
	})

	// Fixed-point BFS from rootTurnID over the in-memory snapshot's
	// parentTurnID edges. N is bounded by the shared performance admission
	// limit, so the repeated O(N) passes
	// below cost nothing in practice.
	reached := map[string]bool{rootTurnID: true}
	var result []*turnState
	for changed := true; changed; {
		changed = false
		for _, ts := range all {
			if reached[ts.turnID] {
				continue
			}
			if reached[ts.parentTurnID] {
				reached[ts.turnID] = true
				result = append(result, ts)
				changed = true
			}
		}
	}
	return result
}

// resolveInterruptTargets resolves id + scope into the concrete set of LIVE
// turnStates a caller's cascade must reach: exactly the anchor(s)
// resolveInterruptAnchors finds for ScopeSelfOnly, or the anchor(s) PLUS
// their live descendants (collectLiveDescendantTurnStates) for ScopeSubtree.
// Shared by Interrupt and InterruptSessionHard so the two escalation stages
// always agree on which turns are in scope (FR-042, AC-8).
func (al *AgentLoop) resolveInterruptTargets(id string, scope InterruptScope) []*turnState {
	anchors := al.resolveInterruptAnchors(id)
	if len(anchors) == 0 || scope == ScopeSelfOnly {
		return anchors
	}
	seen := make(map[string]bool, len(anchors))
	targets := make([]*turnState, 0, len(anchors))
	for _, ts := range anchors {
		if !seen[ts.turnID] {
			seen[ts.turnID] = true
			targets = append(targets, ts)
		}
	}
	for _, anchor := range anchors {
		for _, d := range al.collectLiveDescendantTurnStates(anchor.turnID) {
			if !seen[d.turnID] {
				seen[d.turnID] = true
				targets = append(targets, d)
			}
		}
	}
	return targets
}

// markTurnsCancelling sets turnState.cancelling on every turn in targets —
// the GATE half of the chain-reaction supersession of ADR-057 FR-024 (see
// that field's own doc comment, turn.go, for the full mechanism). Called by
// BOTH Interrupt and InterruptSessionHard, right after resolving targets and
// before any interrupt signal actually fires, so the two cancel surfaces that
// share this resolution (RequestCancel's chat-wide Stop, and
// `delegate action=cancel`'s per-delegate cascade — both ultimately call
// Interrupt/InterruptSessionHard) get the gate uniformly with one change.
// Idempotent: setting an already-true flag is harmless, so calling this from
// both Interrupt (PHASE A) and InterruptSessionHard (PHASE B) for the SAME
// target is not a bug — it is redundant-but-safe, exactly like this file's
// existing resolveInterruptTargets sharing.
func markTurnsCancelling(targets []*turnState) {
	for _, ts := range targets {
		ts.cancelling.Store(true)
	}
}

// Interrupt gracefully cancels the turn(s) addressed by id, according to
// scope. FR-6, FR-10, FR-12a, FR-15, FR-41.
//
// ADR-057 D8/FR-041 collapses the four pre-existing entry points
// (InterruptSession, InterruptSessionHard, InterruptBySessionKey,
// InterruptBySessionKeyHard) into this function (graceful) and
// InterruptSessionHard (hard, same file), both now taking a mandatory
// InterruptScope so the compiler forces every caller to name its intent —
// see resolveInterruptAnchors and InterruptScope's doc comments for the full
// mechanism and the #577 reconciliation.
//
// The cascade spawns a goroutine per resolved target that calls
// requestGracefulInterrupt AND providerCancel in parallel so the in-flight
// LLM HTTP request is aborted immediately (FR-12a) rather than waiting for
// the stream to drain naturally within the 3s graceful window.
//
// Returns the list of turn IDs that received the cancel signal. The cancel
// handler includes this in the turn_canceled audit/transcript entry when
// scope is ScopeSubtree rooted at a chat. Returns an error only if id is
// empty. No matching live turn is not an error — callers treat it as a
// no-op (was_fired=false for a chat-wide Stop; "already terminated" for a
// delegate cancel, whose caller ownership was already verified before
// reaching here).
func (al *AgentLoop) Interrupt(id string, scope InterruptScope, hint string) (descendants []string, err error) {
	if id == "" {
		return nil, fmt.Errorf("empty session_id")
	}

	targets := al.resolveInterruptTargets(id, scope)
	if len(targets) == 0 {
		return nil, nil // no active turn — caller emits turn_cancel_attempt{was_fired:false}
	}
	// [Chain-reaction supersession of ADR-057 FR-024 — the GATE half] Mark
	// every resolved target as cancelling FIRST, before anything else in this
	// function — see turnState.cancelling's doc comment (turn.go) for the
	// full mechanism, INCLUDING the ADR-091 fix lane RX-SUBTURN finding that
	// grep finds no current reader of this flag (cancelling.Load()) anywhere
	// in the repo. This was what actually closed the "a new child born
	// during cancellation escapes it" race: recursion (the fresh re-scan/
	// chain-reaction-latch machinery elsewhere in this file and cancel.go)
	// only ever reaches a child that has ALREADY registered, or is ALREADY
	// known to be imminent — it cannot stop a spawn that has not even been
	// attempted yet. Pre-ADR-091, the deleted spawnSubTurn (subturn.go)
	// checked this flag, walking the parentTurnState ancestor chain, before
	// creating any new child.
	markTurnsCancelling(targets)
	for _, ts := range targets {
		descendants = append(descendants, ts.turnID)
	}

	var wg sync.WaitGroup
	for _, ts := range targets {
		// capture loop variable (Go 1.22+ per-iteration scoping)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.ErrorCF("agent", "Panic in Interrupt goroutine",
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
					ts.eventMeta("Interrupt", "turn.interrupt.received"),
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

// InterruptSessionHard escalates a previously-graceful cancel to a hard
// abort for the turn(s) addressed by id, according to scope. Called at t=3s
// after Interrupt per FR-11. See InterruptHard (below) for the legacy
// process-wide single-turn path, which takes no session id and is out of
// scope for this collapse (FR-041).
//
// ADR-057 D8/FR-041: this is the hard-escalation half of the two-function
// collapse — see Interrupt's doc comment for the full mechanism. It keeps
// the name InterruptSessionHard (one of the four retired names) rather than
// "InterruptHard" specifically to avoid colliding with the existing
// zero-argument process-wide InterruptHard below.
//
// Returns the list of turn IDs that received the hard-abort signal.
func (al *AgentLoop) InterruptSessionHard(id string, scope InterruptScope, hint string) (descendants []string, err error) {
	if id == "" {
		return nil, fmt.Errorf("empty session_id")
	}

	targets := al.resolveInterruptTargets(id, scope)
	if len(targets) == 0 {
		return nil, nil
	}
	// Chain-reaction supersession of ADR-057 FR-024 — GATE half; see
	// Interrupt's identical call site (above) for the full rationale.
	// Idempotent against a target Interrupt already marked moments earlier
	// (the common PHASE-A-then-PHASE-B ordering) — atomic.Bool.Store(true)
	// twice is harmless.
	markTurnsCancelling(targets)
	for _, ts := range targets {
		descendants = append(descendants, ts.turnID)
	}

	var wg sync.WaitGroup
	for _, ts := range targets {
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
			// defensively in case its providerCancel pointer was reset between the
			// two calls (e.g. a new turn started on the same turnState slot).
			if !ts.requestHardAbort() {
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
// (routingSessionID equality — ADR-057 FR-015 role-B predicate, rebased from
// transcriptSessionID; the same predicate Interrupt/InterruptSessionHard's
// whole-chat Range fallback and collectDescendantTurnIDs use) that has NOT
// yet finished (turnState.IsAlive()).
//
// Root cause this closes: RequestCancel's PHASE B/C escalation timers
// (pkg/agent/cancel.go) used to
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
// graceful nudge (also delivered by Interrupt's cascade) only aborts
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
	al.activeTurnStates.Range(func(key, value any) bool {
		ts, ok := value.(*turnState)
		if !ok {
			slog.Error("activeTurnStates contains non-*turnState value",
				"session_key", key, "value_type", fmt.Sprintf("%T", value))
			return true
		}
		if string(ts.routingSessionID) == sessionID && ts.IsAlive() {
			alive = append(alive, ts)
		}
		return true
	})
	return alive
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
//
// dequeuePendingSubTurnResults was deleted 2026-09-24 as an unreachable
// ADR-091 leftover (golangci unused): grep found no caller anywhere in the
// repo.

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
		// Turn-start projection set (ADR-066 FR-020): this turn's emptying
		// is undone in the same write — see turn.go::restoreSession.
		ts.session.RollbackAppended(sessionKey, targetLen, targetSkip, ts.initialEmptiedSet)
		ts.revertEmptiedTranscript()

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
		_, enqErr := al.enqueueSteeringMessage(childSessionKey, childAgentID, providers.Message{
			Role:    "user",
			Content: env.Text,
		}, "")
		return enqErr
	default:
		return fmt.Errorf("%w: kind %q", ErrSessionMessageNotTurnInjectable, kind)
	}
}
