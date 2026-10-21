// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// pairingStopTimeout bounds how long a Stop waits for a pairing client to be
// torn down; Stop must not hang on a stuck platform connection.
const pairingStopTimeout = 10 * time.Second

// ErrPairingUnavailable refuses a pairing admission when no channel manager
// (and so no attempt store) is wired.
var ErrPairingUnavailable = errors.New("pairing ownership is unavailable")

// pairingAttempts returns the channel manager's pairing-attempt store, or nil.
func (al *AgentLoop) pairingAttempts() *channels.PairingAttempts {
	mgr := al.GetChannelManager()
	if mgr == nil {
		return nil
	}
	return mgr.PairingAttempts()
}

func (al *AgentLoop) admitPairing(channelID string, kind channels.PairingAdmission, owner *channels.PairingOwner) (string, error) {
	store := al.pairingAttempts()
	if store == nil {
		return "", ErrPairingUnavailable
	}
	rec, err := store.Admit(channelID, kind, owner)
	if err != nil {
		return "", err
	}
	return rec.AttemptID, nil
}

// AdmitConnectPairing reserves the next pairing attempt of channelID for the
// approved connect_channel call requestID in sessionID, before anything is
// saved (gate r3 M1, M5). channels.ErrPairingConflict refuses it: another
// attempt is pending, starting, running or stopping, and its owner wins.
func (al *AgentLoop) AdmitConnectPairing(channelID, requestID, sessionID string) (string, error) {
	return al.admitPairing(channelID, channels.PairingAdmissionConnect,
		&channels.PairingOwner{SessionID: sessionID, RequestID: requestID})
}

// AdmitRetryPairing reserves a NEW attempt for an authenticated chat Retry
// (gate r3 M4): the chat must own the latest ended attempt, or the attempt a
// gateway restart interrupted. The caller has proved the session belongs to
// the requesting user.
func (al *AgentLoop) AdmitRetryPairing(channelID, requestID, sessionID string) (string, error) {
	return al.admitPairing(channelID, channels.PairingAdmissionRetry,
		&channels.PairingOwner{SessionID: sessionID, RequestID: requestID})
}

// AdmitEnablePairing reserves an unowned attempt for an ordinary enable that
// turns a connector on (gate r3 M1).
func (al *AgentLoop) AdmitEnablePairing(channelID string) (string, error) {
	return al.admitPairing(channelID, channels.PairingAdmissionEnable, nil)
}

// CommitPairing marks an admitted attempt startable once the configuration it
// enables committed, bound to that configuration's revision (M14), and asks
// the next reload to give the connector a fresh start even when its
// configuration is unchanged (M4, M10). A connector the manager does not hold
// yet is started by the reload's own add.
func (al *AgentLoop) CommitPairing(channelID, attemptID string) bool {
	mgr := al.GetChannelManager()
	if mgr == nil {
		return false
	}
	if !mgr.PairingAttempts().MarkReady(channelID, attemptID, channels.ChannelRevision(al.GetConfig(), channelID)) {
		return false
	}
	if _, registered := mgr.GetChannel(channelID); registered {
		mgr.RequestRestart(channelID)
	}
	return true
}

// ReleasePairing ends an admitted attempt that no start consumed: its save or
// enable failed, or a confirmed reload did not start it. An attempt a start
// already took is left to that start (M10). An owned one stays retryable.
func (al *AgentLoop) ReleasePairing(channelID, attemptID string) {
	if store := al.pairingAttempts(); store != nil && attemptID != "" {
		store.Release(channelID, attemptID, channels.PairingOutcomeInterrupted)
	}
}

// RevokePairingForDisable ends a not-yet-started admission of a connector
// being disabled, so it cannot start late (M2).
func (al *AgentLoop) RevokePairingForDisable(channelID string) {
	if store := al.pairingAttempts(); store != nil {
		store.RevokeForDisable(channelID)
	}
}

// BeginPairingDelete and EndPairingDelete bracket a connector's deletion (M12).
func (al *AgentLoop) BeginPairingDelete(channelID string) {
	if store := al.pairingAttempts(); store != nil {
		store.BeginDelete(channelID)
	}
}

func (al *AgentLoop) EndPairingDelete(channelID string, removed bool) {
	if store := al.pairingAttempts(); store != nil {
		store.EndDelete(channelID, removed)
	}
}

// requestChannelRestart asks the next reload to start a registered channel
// that is not running (an approved connect for a connector stopped in place,
// or one whose earlier start failed), through the reload lifecycle (M10).
func (al *AgentLoop) requestChannelRestart(id string) bool {
	mgr := al.GetChannelManager()
	if mgr == nil {
		return false
	}
	ch, registered := mgr.GetChannel(id)
	if !registered || ch.IsRunning() {
		return false
	}
	mgr.RequestRestart(id)
	return true
}

// lastStartError returns a channel's most recent start failure.
func (al *AgentLoop) lastStartError(id string) error {
	mgr := al.GetChannelManager()
	if mgr == nil {
		return nil
	}
	return mgr.LastStartError(id)
}

// channelLiveStatus reports whether a configured channel instance is running
// after the last reload, and a value-free failure reason when it is not.
func (al *AgentLoop) channelLiveStatus(id string) (running bool, failure string, known bool) {
	mgr := al.GetChannelManager()
	if mgr == nil {
		return false, "", false
	}
	if ch, ok := mgr.GetChannel(id); ok {
		return ch.IsRunning(), "", true
	}
	for _, failed := range mgr.FailedChannels() {
		if failed.Name == id {
			if failed.CredentialScoped {
				return false, "a configured credential reference is missing from the vault", true
			}
			return false, "the channel did not start: the platform rejected the credentials or could not be reached", true
		}
	}
	return false, "the channel is not running", true
}

// EmitWhatsAppPairing publishes a pairing update of channelID's latest
// attempt. Channels that report their own attempt id use
// EmitWhatsAppPairingAttempt.
func (al *AgentLoop) EmitWhatsAppPairing(channelID string, status channels.PairingStatus, qr, message string) {
	al.EmitWhatsAppPairingAttempt(channelID, "", status, qr, message)
}

// EmitWhatsAppPairingAttempt publishes one pairing event of attemptID (empty:
// the channel's latest attempt) onto the event bus, so connected SPA clients
// receive a whatsapp_pairing frame (#283). Only the connector's current
// attempt is published (gate r3 M8): progress only while it is active, a
// terminal event only for the attempt that event ended; a late event of an
// older attempt never reaches a successor's frames or cache. The frame and its
// audit row carry that attempt's own immutable owner (M13).
func (al *AgentLoop) EmitWhatsAppPairingAttempt(channelID, attemptID string, status channels.PairingStatus, qr, message string) {
	al.pairingEmitMu.Lock()
	defer al.pairingEmitMu.Unlock()
	store := al.pairingAttempts()
	if store == nil {
		al.publishPairingLocked(channelID, "", nil, status, qr, message)
		return
	}
	if attemptID == "" {
		latest, ok := store.Latest(channelID)
		if !ok {
			// No attempt is known (a channel outside attempt tracking).
			al.publishPairingLocked(channelID, "", nil, status, qr, message)
			return
		}
		attemptID = latest.AttemptID
	}
	rec, ok := store.Attempt(channelID, attemptID)
	latest, hasLatest := store.Latest(channelID)
	if !ok || !hasLatest || latest.AttemptID != attemptID {
		return // an older attempt: never published over its successor
	}
	switch status {
	case channels.PairingStatusWaiting, channels.PairingStatusCode:
		if rec.Phase != channels.PairingPhaseActive {
			return
		}
	default:
		if rec.Phase == channels.PairingPhaseActive {
			// An event of a channel that does not complete its own attempts.
			done, applied := store.Complete(channelID, attemptID, outcomeOfStatus(status))
			if !applied {
				return
			}
			rec = done
		}
		if rec.Phase != channels.PairingPhaseTerminal {
			return // a stopping attempt's result is published by its Stop
		}
	}
	al.publishPairingLocked(channelID, attemptID, rec.Owner, status, qr, message)
}

func outcomeOfStatus(status channels.PairingStatus) channels.PairingOutcome {
	switch status {
	case channels.PairingStatusLinked:
		return channels.PairingOutcomeLinked
	case channels.PairingStatusTimeout:
		return channels.PairingOutcomeTimeout
	case channels.PairingStatusStopped:
		return channels.PairingOutcomeStopped
	default:
		return channels.PairingOutcomeError
	}
}

// publishPairingLocked emits one pairing frame and, for a terminal status, its
// audit row. Caller holds pairingEmitMu.
func (al *AgentLoop) publishPairingLocked(channelID, attemptID string, owner *channels.PairingOwner, status channels.PairingStatus, qr, message string) {
	payload := WhatsAppPairingPayload{
		ChannelID: channelID,
		Status:    status,
		QR:        qr,
		Message:   message,
		Seq:       al.pairingSeq.Add(1),
	}
	if owner != nil {
		payload.RequestID, payload.SessionID = owner.RequestID, owner.SessionID
	}
	if store := al.pairingAttempts(); store != nil && attemptID != "" {
		if rec, ok := store.Attempt(channelID, attemptID); ok {
			payload.RestartInterrupted = rec.RestartInterrupted
		}
	}
	al.emitEvent(EventKindWhatsAppPairing, EventMeta{Source: "channel"}, payload)
	// #358, FR-081/K10: audit-log device-pairing lifecycle transitions so linking a
	// new WhatsApp device leaves a tamper-evident trail. We deliberately do NOT log
	// the `code`/`connecting` states (high-frequency, and the QR itself is a
	// scannable secret that must never reach the audit file) — only the terminal
	// outcomes. Decision uses the audit vocabulary (allow=linked, error=failed/
	// expired, deny=stopped by the person — gate r1 M6, security T2-S5); the exact
	// pairing status rides in Details, with the attempt id (gate r3 M13) and the
	// chat request/session ids of an owned attempt.
	switch status {
	case channels.PairingStatusLinked, channels.PairingStatusError, channels.PairingStatusTimeout,
		channels.PairingStatusStopped:
		decision := audit.DecisionAllow
		switch status {
		case channels.PairingStatusError, channels.PairingStatusTimeout:
			decision = audit.DecisionError
		case channels.PairingStatusStopped:
			decision = audit.DecisionDeny
		}
		details := map[string]any{
			"channel": channelID,
			"status":  string(status),
			"message": message,
		}
		if attemptID != "" {
			details["attempt_id"] = attemptID
		}
		if owner != nil {
			details["request_id"] = owner.RequestID
			details["session_id"] = owner.SessionID
		}
		audit.EmitEntry(al.auditLogger, &audit.Entry{
			Timestamp: time.Now().UTC(),
			Event:     audit.EventChannelPairing,
			Decision:  decision,
			Details:   details,
		})
	}
}

// pairingStopSelection collects the pairing attempts one Stop selected at each
// reached session's admission (gate r3 M6, M9). It is frozen: Stop never
// re-enumerates "whatever this session owns" later (M7).
type pairingStopSelection struct {
	mu       sync.Mutex
	attempts []channels.PairingAttempt
	seen     map[string]bool
}

func (s *pairingStopSelection) add(attempts []channels.PairingAttempt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = make(map[string]bool)
	}
	for _, a := range attempts {
		if !s.seen[a.AttemptID] {
			s.seen[a.AttemptID] = true
			s.attempts = append(s.attempts, a)
		}
	}
}

func (s *pairingStopSelection) all() []channels.PairingAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]channels.PairingAttempt(nil), s.attempts...)
}

// selectOwnedPairings captures the in-flight attempts sessionID owns at its
// Stop admission.
func (al *AgentLoop) selectOwnedPairings(sessionID string) []channels.PairingAttempt {
	store := al.pairingAttempts()
	if store == nil || sessionID == "" {
		return nil
	}
	return store.OwnedBy(sessionID)
}

// stopSelectedPairings ends exactly the attempts a Stop selected (BDD-8.8,
// gate r3 M6–M8). Each is re-checked atomically (same attempt, same owner,
// still stoppable): a started one becomes stopping and only its own captured
// teardown runs, never a channel looked up by name; one that never started is
// revoked. The stopped frame and audit row carry the selected attempt's own
// owner. A teardown that did not finish stays stopping and is reported.
func (al *AgentLoop) stopSelectedPairings(selected []channels.PairingAttempt) {
	store := al.pairingAttempts()
	if store == nil {
		return
	}
	for _, attempt := range selected {
		sel, ok := store.BeginStop(attempt)
		if !ok {
			continue // no longer that attempt, or not stoppable: a no-op
		}
		if sel.Cancel == nil {
			al.pairingEmitMu.Lock()
			al.publishPairingLocked(sel.Attempt.Instance, sel.Attempt.AttemptID, sel.Attempt.Owner,
				channels.PairingStatusStopped, "", "pairing stopped from the chat")
			al.pairingEmitMu.Unlock()
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), pairingStopTimeout)
		teardownErr := sel.Cancel(ctx)
		cancel()
		al.pairingEmitMu.Lock()
		done, finished := store.FinishStop(sel.Attempt.Instance, sel.Attempt.AttemptID, teardownErr)
		if finished {
			al.publishPairingLocked(done.Instance, done.AttemptID, done.Owner,
				channels.PairingStatusStopped, "", "pairing stopped from the chat")
		}
		al.pairingEmitMu.Unlock()
		if !finished {
			fields := map[string]any{"channel_id": sel.Attempt.Instance, "attempt_id": sel.Attempt.AttemptID}
			if teardownErr != nil {
				fields["error"] = teardownErr.Error()
			}
			logger.ErrorCF("agent", "Stopping the pairing did not finish; it stays stopping and no new pairing starts until it does. Restart the channel or the gateway if it persists", fields)
			continue
		}
		logger.InfoCF("agent", "Stopped an in-flight channel pairing on Stop",
			map[string]any{"channel_id": sel.Attempt.Instance, "attempt_id": sel.Attempt.AttemptID})
	}
}
