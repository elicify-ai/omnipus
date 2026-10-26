// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// secretWakeInput validates an internal wake without claiming delivery. The
// request/session/account binding is rechecked at actual consumption too.
func (al *AgentLoop) secretWakeInput(msg bus.InboundMessage) (secretrequest.Record, error) {
	reg := al.SecretRequests()
	if reg == nil {
		return secretrequest.Record{}, errors.New("secret continuation service is unavailable")
	}
	rec, err := reg.Get(inboundMetadata(msg, secretResumeMetadataKey), msg.GatewayUserID)
	if err != nil {
		return secretrequest.Record{}, err
	}
	if rec.Card.State != "submitted" || rec.ResumeStopped || rec.Card.SessionId != msg.AsyncTranscriptSessionID || rec.Card.ActingAgentId != msg.AsyncOriginAgentID {
		return secretrequest.Record{}, errors.New("secret continuation binding is unavailable")
	}
	return rec, nil
}

// enqueueLiveSecretWake uses the existing checked steering queue of an
// already-admitted helper. It never takes the human-helper revival shortcut.
func (al *AgentLoop) prepareSecretSteeredWake(ctx context.Context, msg *bus.InboundMessage, rec *session.LifecycleRecord, generation int) (*session.LifecycleRecord, int, bool, error) {
	if inboundMetadata(*msg, secretResumeMetadataKey) == "" {
		return rec, generation, false, nil
	}
	secretRecord, err := al.secretWakeInput(*msg)
	if err != nil {
		return rec, generation, false, err
	}
	msg.Content = secretrequest.ResumeMessage(secretRecord)
	if secretRestartStopped(rec) {
		if al.getActiveTurnState(rec.SessionID) != nil {
			return rec, generation, false, errors.New("restart interruption has not retired its execution yet")
		}
		rec, err = al.reviveSecretRestart(ctx, rec)
		if err != nil {
			return rec, generation, false, err
		}
		generation = rec.Generation
	}
	if rec.Stopped() {
		return rec, generation, false, errors.New("secret continuation cannot resume a stopped session")
	}
	if generation != rec.Generation {
		return rec, generation, false, errors.New("secret continuation generation changed")
	}
	if rec.SteeredBy != nil && al.getActiveTurnState(rec.SessionID) != nil {
		return rec, generation, true, al.enqueueLiveSecretWake(secretRecord)
	}
	return rec, generation, false, nil
}

func (al *AgentLoop) claimAdmittedSecretWake(id, sessionID string, ts *turnState) (bool, error) {
	if id == "" {
		return true, nil
	}
	rec, claimed, err := al.SecretRequests().ClaimResume(id, sessionID)
	if err != nil || !claimed {
		return claimed, err
	}
	ts.opts.UserMessage = secretrequest.ResumeMessage(rec)
	ts.userMessage = ts.opts.UserMessage
	return true, nil
}

func (al *AgentLoop) enqueueLiveSecretWake(rec secretrequest.Record) error {
	_, err := al.enqueueSteeringItemWithStatus(rec.Card.SessionId, rec.Card.ActingAgentId, steeringQueueItem{
		message:         providers.Message{Role: "user", Content: secretrequest.ResumeMessage(rec)},
		secretRequestID: rec.Card.RequestId,
	}, func() EnqueueStatus { return EnqueueStatusPostFinish })
	return err
}

func (al *AgentLoop) claimSecretSteeringInput(scope, id string) (providers.Message, bool, error) {
	reg := al.SecretRequests()
	if reg == nil {
		return providers.Message{}, false, errors.New("secret continuation service is unavailable")
	}
	rec, claimed, err := reg.ClaimResume(id, normalizeSteeringScope(scope))
	if err != nil || !claimed {
		return providers.Message{}, claimed, err
	}
	return providers.Message{Role: "user", Content: secretrequest.ResumeMessage(rec)}, true, nil
}

// queuedSecretWakeItems reads the items owned by THIS admitted execution. It
// neither borrows another generation's queue nor consumes before promotion.
func (al *AgentLoop) queuedSecretWakeItems(claim executionClaim) []steeringQueueItem {
	gate := al.steerAdmission()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	entry, ok := gate.active[claim.SessionID]
	if !ok || entry.executionClaim() != claim {
		return nil
	}
	out := make([]steeringQueueItem, 0, len(entry.wakeInputs))
	for _, item := range entry.wakeInputs {
		if item.secretRequestID != "" {
			out = append(out, item)
		}
	}
	return out
}

// consumePromotedSecretWakes runs only after the usual live reservation and
// Stop checks. Reconstructed launch text is not re-injected for a secret-only
// wake; only the claimed reference messages enter the existing turn body.
func (al *AgentLoop) consumePromotedSecretWakes(ts *turnState, claim executionClaim, items []steeringQueueItem, hasOrdinaryInput bool) error {
	if len(items) == 0 {
		return nil
	}
	msgs, ids, _, err := al.consumeDequeuedSteeringResult(claim.SessionID, items)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return errors.New("secret continuation was already consumed")
	}
	if !hasOrdinaryInput {
		ts.opts.UserMessage, ts.userMessage, ts.opts.InputAlreadyArchived = "", "", false
	}
	ts.opts.UserInitiated = false
	ts.opts.InitialSteeringMessages = append(ts.opts.InitialSteeringMessages, msgs...)
	ts.opts.InitialSteeringCorrelationIDs = append(ts.opts.InitialSteeringCorrelationIDs, ids...)
	return nil
}

// secretRestartStopped is deliberately narrower than the general interrupted
// display predicate: only a durable Restart cause authorizes this exception.
func secretRestartStopped(rec *session.LifecycleRecord) bool {
	return rec != nil && rec.StopNote != nil && rec.StopNote.Cause == session.StopCauseRestart && (rec.Stopped() || rec.Terminal())
}

// reviveSecretRestart uses the established revival mutation with a live,
// atomic cause precondition. A later explicit Stop is never cleared.
func (al *AgentLoop) reviveSecretRestart(ctx context.Context, rec *session.LifecycleRecord) (*session.LifecycleRecord, error) {
	if !secretRestartStopped(rec) {
		return rec, nil
	}
	_, err := al.steerCanceller().reviveWithExecution(ctx, rec.SessionID, ownerWakeRevivalPrincipal, nil, nil, func(current *session.LifecycleRecord) error {
		if current == nil || current.Generation != rec.Generation || !secretRestartStopped(current) {
			return errors.New("secret continuation cannot override an explicit Stop or changed execution")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return al.GetSessionLifecycleStore().Load(rec.SessionID)
}

func secretResumeIDFromMessage(messageID string) string {
	const prefix = "secret-resume-"
	if strings.HasPrefix(messageID, prefix) {
		return strings.TrimPrefix(messageID, prefix)
	}
	return ""
}
