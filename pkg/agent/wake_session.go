package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// wakeSource carries an authenticated producer's source event, never a claim
// that a row was already written. The row writer independently saves/dedupes it.
type wakeSource struct {
	childID        string
	event          *generated.HelperEvent
	restartBootSeq uint64
	// publishKind is the descriptive bus kind a producer has always used
	// (the upward message's own kind, or "error" for a stop notice). It is
	// not a wake reason and does not change eligibility.
	publishKind string
}
type wakeSourceKey struct{}

var errWakeRecipientUnavailable = errors.New("wake recipient unavailable")

// WakeSessionResult keeps a held, consumed, pending or deliberately suppressed
// input distinct from a failed publication. It is internal control flow, not a
// wire format; the upward tool surface retains its existing Delivery contract.
type WakeSessionResult struct {
	steer.Delivery
	Disposition string
	RowPending  bool
}

// WakeSession is the shared outcome/creator-notice entry. Saving is chat
// membership; only an admitted consumer may place it into model history.
func (al *AgentLoop) WakeSession(ctx context.Context, recipientSessionID, reason, outcomeID, body string) (result WakeSessionResult, retErr error) {
	result = WakeSessionResult{Delivery: steer.Delivery{MessageID: outcomeID, Outcome: steer.DeliveryStoredNotWoken}, Disposition: "failed"}
	var rowErr error
	defer func() {
		result.RowPending = rowErr != nil
		retErr = errors.Join(retErr, rowErr)
	}()
	switch reason {
	case "finished", "failed", "timed_out", "verdict", "question", "blocker", "restart", "creator_notice":
	default:
		return result, fmt.Errorf("wake session: reason %q is not supported", reason)
	}
	if strings.TrimSpace(recipientSessionID) == "" || strings.TrimSpace(outcomeID) == "" || strings.TrimSpace(body) == "" {
		return result, fmt.Errorf("wake session: recipient, outcome id and body are required")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	meta, rec, err := al.wakeRecipient(recipientSessionID)
	if err != nil {
		return result, err
	}
	if _, dispositionErr := al.GetSessionStore().SavedInputConsumed(recipientSessionID, "saved-input-"+outcomeID); dispositionErr != nil {
		if expired, finishErr := al.finishExpiredSavedWake(ctx, recipientSessionID, outcomeID, dispositionErr); expired {
			result.Disposition = "expired_undelivered"
			return result, finishErr
		}
		return result, dispositionErr
	}
	body = al.filterWakeText(body)
	if err = al.saveWakeEvent(ctx, recipientSessionID, reason, outcomeID, body); err != nil {
		var pending *helperEventRowPendingError
		if !errors.As(err, &pending) {
			return result, err
		}
		// The independent repair payload is durable. A missing chat row is
		// reported as pending, not as shown, without losing the input/wake.
		rowErr = err
	}
	if _, err = al.GetSessionStore().SaveChatInput(recipientSessionID, "saved-input-"+outcomeID, rec.AgentID, body); err != nil {
		if expired, finishErr := al.finishExpiredSavedWake(ctx, recipientSessionID, outcomeID, err); expired {
			result.Disposition = "expired_undelivered"
			return result, finishErr
		}
		if errors.Is(err, session.ErrAlreadyConsumed) {
			result.Disposition = "already_consumed"
			al.releaseWakePublication(recipientSessionID, outcomeID)
			return result, al.ackConsumedSteeringWake(steeringWake{messageID: outcomeID, transcriptSessionID: recipientSessionID})
		}
		return result, fmt.Errorf("wake session: save input: %w", err)
	}
	if source, ok := ctx.Value(wakeSourceKey{}).(wakeSource); ok {
		if err = al.ackSavedEndingVerdict(source, recipientSessionID, outcomeID); err != nil {
			return result, err
		}
	}
	result.Disposition = "saved_not_placed"
	consumed, err := al.wakeInputConsumed(recipientSessionID, outcomeID)
	if err != nil {
		if expired, finishErr := al.finishExpiredSavedWake(ctx, recipientSessionID, outcomeID, err); expired {
			result.Disposition = "expired_undelivered"
			return result, finishErr
		}
		return result, err
	}
	if consumed {
		if err = al.GetSessionStore().RetireSavedInput(recipientSessionID, "saved-input-"+outcomeID); err != nil {
			return result, err
		}
		result.Disposition = "already_consumed"
		return result, nil
	}
	rec, err = al.reopenRestartWakeRecipient(ctx, rec)
	if err != nil {
		return result, err
	}
	blocked, err := al.wakeBlocked(rec)
	if err != nil {
		return result, err
	}
	if blocked {
		result.Disposition = "suppressed_by_stop"
		return result, nil
	}
	// PW-D15: only an UNANSWERED card holds the outcome (a retained answered
	// or cancelled record does not), and it holds even while the turn that
	// parked it is still unwinding. A live tool approval is not a card.
	cardOpen, err := questionCardOpen(recipientSessionID, meta.PendingAskJSON)
	if err != nil {
		return result, err
	}
	live := al.liveTurnForSession(recipientSessionID)
	if cardOpen || ((live == nil || !live.IsAlive()) && rec.State == session.LifecycleNeedsInput) {
		result.Disposition = "held_for_question"
		return result, nil
	}
	if al.asyncNotifier == nil {
		return result, fmt.Errorf("wake session: notifier is not configured")
	}
	key := recipientSessionID + "\x00" + outcomeID
	if _, pending := al.asyncNotifier.sessionWakes.LoadOrStore(key, true); pending {
		result.Disposition = "already_pending"
		return result, nil
	}
	published := false
	defer func() {
		if !published {
			al.asyncNotifier.sessionWakes.Delete(key)
		}
	}()
	if live != nil && live.IsAlive() {
		if steerUpwardDelivererAfterLiveTurnCheckTestHook != nil {
			steerUpwardDelivererAfterLiveTurnCheckTestHook(recipientSessionID, rec.Generation)
		}
		// Enqueue on the live turn's own scope: an ordinary root is registered
		// under its agent-scoped key, not the bare session id.
		err = al.EnqueueSteeringWake(live.sessionKey, rec.AgentID, recipientSessionID, outcomeID,
			providers.Message{Role: "user", Content: body})
		if err == nil {
			published = true
			result.Outcome = steer.DeliveryQueuedIntoLiveTurn
			result.Disposition = "joined_live_turn"
			return result, nil
		}
		current, loadErr := al.GetSessionLifecycleStore().Load(recipientSessionID)
		// The live turn closed its steering scope between the live check and
		// the enqueue. The same execution is either still finishing (not yet
		// terminal) or an ordinary root that just completed; both fall through
		// to one idle wake so the outcome is not dropped. A terminal helper or
		// a changed generation is never reopened.
		justCompletedRoot := current != nil && current.SteeredBy == nil && current.State == session.LifecycleCompleted
		if loadErr != nil || !errors.Is(err, errSteeringScopeClosed) || current.Generation != rec.Generation ||
			(current.Terminal() && !justCompletedRoot) {
			return result, fmt.Errorf("wake session: join live turn: %w", errors.Join(err, loadErr))
		}
	}
	// Re-read Stop authority after the live-turn race, before publication.
	if rec, err = al.GetSessionLifecycleStore().Load(recipientSessionID); err != nil {
		return result, err
	}
	if blocked, err = al.wakeBlocked(rec); err != nil {
		return result, err
	}
	if blocked {
		result.Disposition = "suppressed_by_stop"
		return result, nil
	}
	channel, chatID := wakeStreamAddress(meta)
	publishKind := reason
	if source, ok := ctx.Value(wakeSourceKey{}).(wakeSource); ok && source.publishKind != "" {
		publishKind = source.publishKind
	}
	err = al.asyncNotifier.WakeParentAlways(ctx, publishKind, tools.MessageParentWakeEvent{
		Channel: channel, ChatID: chatID, AgentID: rec.AgentID, TranscriptSessionID: recipientSessionID,
		Content: body, MessageID: outcomeID, Generation: rec.Generation,
	})
	if err != nil {
		return result, fmt.Errorf("wake session: publish: %w", err)
	}
	published = true
	result.Outcome = steer.DeliveryWoke
	result.Disposition = "published_idle_wake"
	return result, nil
}

func (al *AgentLoop) wakeRecipient(id string) (*session.UnifiedMeta, *session.LifecycleRecord, error) {
	store, lifecycle := al.GetSessionStore(), al.GetSessionLifecycleStore()
	if store == nil || lifecycle == nil {
		return nil, nil, fmt.Errorf("wake session: session stores not configured")
	}
	meta, err := store.GetMeta(id)
	if err != nil {
		return nil, nil, fmt.Errorf("wake session: recipient %q: %w", id, errors.Join(errWakeRecipientUnavailable, err))
	}
	if _, ok := al.GetRegistry().GetAgent(meta.AgentID); !ok {
		return nil, nil, fmt.Errorf("wake session: recipient's agent %q is removed", meta.AgentID)
	}
	if meta.Type == session.SessionTypeMain {
		deps := al.loadAddressDeps()
		if deps == nil {
			return nil, nil, fmt.Errorf("wake session: main eligibility unavailable")
		}
		eligible, eligibleErr := deps.PairEligible(meta.WorkspaceID, meta.AgentID)
		if eligibleErr != nil || !eligible {
			return nil, nil, fmt.Errorf("wake session: main recipient is not eligible: %w", errors.Join(eligibleErr, errors.New("hidden or removed main")))
		}
	}
	rec, err := lifecycle.Load(id)
	if errors.Is(err, session.ErrLifecycleNotFound) && !lifecycle.Exists(id) {
		gate := al.steerAdmission()
		gate.entryMu.Lock()
		rec, err = al.ensureOrdinaryRootRecord(lifecycle, id, store)
		gate.entryMu.Unlock()
	}
	if err != nil {
		return nil, nil, fmt.Errorf("wake session: recipient lifecycle: %w", err)
	}
	return meta, rec, nil
}

func (al *AgentLoop) wakeBlocked(rec *session.LifecycleRecord) (bool, error) {
	if lifecycleInFlightStopFence(rec) || rec.State == session.LifecycleStopped {
		return true, nil
	}
	intents, err := al.GetSessionLifecycleStore().UnfinishedStopIntents(rec.SessionID)
	if err != nil {
		return true, err
	}
	return acceptedStopSelectsExecution(rec, intents), nil
}

func (al *AgentLoop) wakeInputConsumed(sessionID, outcomeID string) (bool, error) {
	store := al.GetSessionStore()
	consumed, dispositionErr := store.SavedInputConsumed(sessionID, "saved-input-"+outcomeID)
	if dispositionErr != nil || consumed {
		return consumed, dispositionErr
	}
	_, placed, err := store.SavedChatInput(sessionID, "saved-input-"+outcomeID)
	if err != nil || placed {
		return placed, err
	}
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.ID == "consumed-"+outcomeID || entry.Content == "consumed "+outcomeID {
			return true, nil
		}
	}
	return false, nil
}

func (al *AgentLoop) saveWakeEvent(ctx context.Context, recipient, reason, outcomeID, body string) error {
	if source, ok := ctx.Value(wakeSourceKey{}).(wakeSource); ok {
		if source.event != nil {
			return al.retainHelperEvent(source.childID, recipient, outcomeID, *source.event)
		}
		return al.repairGoalOutcome(source.childID)
	}
	if reason == "creator_notice" || reason == "verdict" {
		return nil
	}
	kind := generated.HelperEventKind(reason)
	if reason == "restart" {
		kind = generated.HelperEventKindRestarted
	}
	childID := strings.SplitN(outcomeID, ":", 2)[0]
	return al.saveHelperEvent(recipient, outcomeID, generated.HelperEvent{
		HelperSessionId: childID, Kind: kind, Text: body, CreatedAt: time.Now().UTC(),
	})
}

// liveTurnForSession resolves the turn currently running for a transcript
// session. A steered helper is registered under its bare session id; an
// ordinary root is registered under its agent-scoped key
// (agent:<agent>:session:<id>), so the exact lookup alone misses a working
// main and would publish an idle wake instead of joining it.
func (al *AgentLoop) liveTurnForSession(sessionID string) *turnState {
	if ts := al.getActiveTurnState(sessionID); ts != nil {
		return ts
	}
	var root *turnState
	al.activeTurnStates.Range(func(_, value any) bool {
		ts, ok := value.(*turnState)
		if !ok || ts.transcriptSessionID != sessionID {
			return true
		}
		if ts.depth == 0 || ts.parentTurnID == "" {
			root = ts
			return false
		}
		return true
	})
	return root
}
