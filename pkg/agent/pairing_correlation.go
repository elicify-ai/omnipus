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
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// pairingStopTimeout bounds how long a Stop waits for a pairing client to be
// torn down; Stop must not hang on a stuck platform connection.
const pairingStopTimeout = 10 * time.Second

// pairingStopRetryDelays spaces the retried teardowns of a pairing whose Stop
// did not finish (gate r4 T2-SF-14): the attempt stays stopping until its
// client is disposed, then ends stopped. A variable so tests can shorten it.
var pairingStopRetryDelays = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute}

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
	// gate r5 R5-A05 / r6: a ready attempt committed at another revision than
	// the connector's current configuration can never start. The next
	// admission closes it as interrupted — telling its chat — and is accepted;
	// an older reload is never the one that decides.
	if cfg := al.GetConfig(); cfg != nil {
		if closed, ok := store.InterruptStaleReady(channelID, channels.ChannelRevision(cfg, channelID)); ok {
			al.publishInterrupted(closed)
		}
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

// admitEnablePairingForTool is enable_channel's admission: with no channel
// manager wired nothing can start, so there is nothing to reserve.
func (al *AgentLoop) admitEnablePairingForTool(channelID string) (string, error) {
	attemptID, err := al.AdmitEnablePairing(channelID)
	if errors.Is(err, ErrPairingUnavailable) {
		return "", nil
	}
	return attemptID, err
}

// CommitPairing marks an admitted attempt startable once the configuration it
// enables committed, bound to that configuration's revision (M14), and asks
// a reload to give the connector a fresh start for exactly that attempt even
// when its configuration is unchanged (M4, M10); the restart is dropped if a
// Stop, disable or delete revokes the attempt first (gate r4 R4-A01). A
// connector the manager does not hold yet is started by the reload's own add.
func (al *AgentLoop) CommitPairing(channelID, attemptID string) bool {
	mgr := al.GetChannelManager()
	if mgr == nil {
		return false
	}
	store := mgr.PairingAttempts()
	order := store.LockChannel(channelID)
	if order.CommittedStart(attemptID) {
		order.Unlock()
		return true // the same committed operation already ran; never restart it
	}
	ready := store.IsReady(channelID, attemptID)
	if !ready {
		// Direct admissions (including the internal caller controls) commit the
		// current exact snapshot. Ordered writers already committed readiness and
		// never enter this branch or bind against a later GetConfig.
		err := al.MutateConfig(func(candidate *config.Config) error {
			if err := store.CheckConfigSnapshot(candidate); err != nil {
				return err
			}
			if !order.MarkReady(attemptID, channels.ChannelRevision(candidate, channelID)) {
				return channels.ErrPairingConflict
			}
			candidate.SetChannelOrderGeneration(channelID, order.Generation())
			order.BindReadySnapshot(candidate)
			return nil
		})
		ready = err == nil
	}
	order.Unlock()
	if !ready {
		return false
	}
	if _, registered := mgr.GetChannel(channelID); registered {
		mgr.RequestRestart(channelID, attemptID)
		if err := al.TriggerReload(); err != nil && !errors.Is(err, ErrReloadNotConfigured) &&
			!errors.Is(err, ErrReloadAlreadyInProgress) {
			logger.WarnCF("agent", "Could not request the reload that starts an admitted pairing; the next reload starts it",
				map[string]any{"channel_id": channelID, "attempt_id": attemptID, "error": err.Error()})
		}
	}
	return true
}

// PairingAttemptState returns attemptID's current record — for a connect
// result that reports what its own admitted start actually did (gate r4
// R4-A05): a running pairing, an already-linked device, or a failed start.
func (al *AgentLoop) PairingAttemptState(channelID, attemptID string) (channels.PairingAttempt, bool) {
	store := al.pairingAttempts()
	if store == nil || attemptID == "" {
		return channels.PairingAttempt{}, false
	}
	return store.Attempt(channelID, attemptID)
}

// ReleasePairing ends an admitted attempt that no start consumed: its save or
// enable failed, or a confirmed reload did not start it. An attempt a start
// already took is left to that start (M10). An owned one stays retryable.
func (al *AgentLoop) ReleasePairing(channelID, attemptID string) {
	if store := al.pairingAttempts(); store != nil && attemptID != "" {
		store.Release(channelID, attemptID, channels.PairingOutcomeInterrupted)
	}
}

// SettlePairing closes an admitted attempt that its own confirmed reload did
// not start — its configuration was replaced meanwhile, so it never will — and
// tells its chat (gate r5 R5-A05). A started, linked or already-closed attempt
// is left as it is.
func (al *AgentLoop) SettlePairing(channelID, attemptID string) {
	store := al.pairingAttempts()
	if store == nil || attemptID == "" {
		return
	}
	order := store.LockChannel(channelID)
	var progress []channels.PairingProgress
	if rec, ok := store.Attempt(channelID, attemptID); ok && rec.Phase == channels.PairingPhaseReady &&
		order.Release(attemptID, channels.PairingOutcomeInterrupted) {
		rec.Phase, rec.Outcome = channels.PairingPhaseTerminal, channels.PairingOutcomeInterrupted
		progress = append(progress, order.Transition(rec, channels.PairingStatusError, channels.PairingInterruptedMessage))
	}
	order.Unlock()
	al.PublishPairingProgress(progress)
}

// RevokePairingForDisable ends a not-yet-started admission of a connector
// being disabled, so it cannot start late (M2).
func (al *AgentLoop) RevokePairingForDisable(channelID string) {
	if store := al.pairingAttempts(); store != nil {
		if revoked, ok := store.RevokeForDisable(channelID); ok {
			al.publishInterrupted(revoked)
		}
	}
}

// publishInterrupted tells an attempt's chat (or Connectors) that a disable or
// delete cut it off, so no card waits on a code that cannot come (gate r4).
func (al *AgentLoop) publishInterrupted(attempt channels.PairingAttempt) {
	al.publishPairingTransition(attempt, channels.PairingStatusError, channels.PairingInterruptedMessage)
}

func (al *AgentLoop) publishPairingTransition(attempt channels.PairingAttempt, status channels.PairingStatus, message string) {
	store := al.pairingAttempts()
	if store == nil {
		return
	}
	order := store.LockChannel(attempt.Instance)
	progress := order.Transition(attempt, status, message)
	order.Unlock()
	al.PublishPairingProgress([]channels.PairingProgress{progress})
}

// BeginPairingDelete and EndPairingDelete bracket a connector's deletion (M12).
func (al *AgentLoop) BeginPairingDelete(channelID string) {
	if store := al.pairingAttempts(); store != nil {
		if revoked, ok := store.BeginDelete(channelID); ok {
			al.publishInterrupted(revoked)
		}
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
	mgr.RequestRestart(id, "")
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
	store := al.pairingAttempts()
	if store == nil {
		al.emitEvent(EventKindWhatsAppPairing, EventMeta{Source: "channel"}, WhatsAppPairingPayload{
			ChannelID: channelID, Status: status, QR: qr, Message: message,
		})
		return
	}
	order := store.LockChannel(channelID)
	progress, accepted := order.Progress(attemptID, status, qr, message)
	order.Unlock()
	if accepted {
		al.PublishPairingProgress([]channels.PairingProgress{progress})
	}
}

// PublishPairingProgress emits already-accepted immutable envelopes AFTER the
// writer/order locks release. The synchronous cache tap and final enqueue each
// return to the same channel guard; emission itself owns no ordering mutex.
func (al *AgentLoop) PublishPairingProgress(progress []channels.PairingProgress) {
	for _, p := range progress {
		al.publishPairingProgress(p)
		if p.Dispose != nil {
			if store := al.pairingAttempts(); store != nil {
				go al.disposeReplacedPairing(store, p)
			}
		}
	}
}

func (al *AgentLoop) publishPairingProgress(p channels.PairingProgress) {
	attempt := p.Attempt
	channelID, attemptID, owner := attempt.Instance, attempt.AttemptID, attempt.Owner
	status, message, outcome := p.Status, p.Message, attempt.Outcome
	payload := WhatsAppPairingPayload{
		ChannelID: channelID, Status: status, QR: p.QR, Message: message,
		Generation: p.Generation, AttemptID: attemptID, Seq: p.Sequence,
		RestartInterrupted: attempt.RestartInterrupted,
	}
	if owner != nil {
		payload.RequestID, payload.SessionID = owner.RequestID, owner.SessionID
	}
	if p.Sequence != 0 {
		al.emitEvent(EventKindWhatsAppPairing, EventMeta{Source: "channel"}, payload)
	}
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
		if outcome != "" {
			details["outcome"] = string(outcome)
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
// owner, and a public frame is published only while the attempt is still the
// connector's latest (gate r4 R4-A03). A teardown that did not finish stays
// stopping, is reported to its chat and is retried until the client is
// disposed (gate r4 T2-SF-14).
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
			if al.pairingRevokeHook != nil {
				al.pairingRevokeHook() // test seam: the window after revocation
			}
			al.publishPairingTransition(sel.Attempt, channels.PairingStatusStopped, "pairing stopped from the chat")
			continue
		}
		if !al.finishPairingTeardown(store, sel, true) {
			go al.retryPairingTeardown(store, sel.Attempt)
		}
	}
}

// finishPairingTeardown runs a selected attempt's own teardown and publishes
// its result. On failure the attempt stays stopping (no successor over an
// undisposed client); the first failure is shown to its chat.
func (al *AgentLoop) finishPairingTeardown(store *channels.PairingAttempts, sel channels.PairingStopSelection, first bool) bool {
	ctx, cancel := context.WithTimeout(context.Background(), pairingStopTimeout)
	teardownErr := sel.Cancel(ctx)
	cancel()
	done, finished := store.FinishStop(sel.Attempt.Instance, sel.Attempt.AttemptID, teardownErr)
	switch {
	case finished:
		al.publishPairingTransition(done, channels.PairingStatusStopped, "pairing stopped from the chat")
	case first && teardownErr != nil:
		al.publishPairingTransition(sel.Attempt, channels.PairingStatusError, channels.PairingStopPendingMessage)
	}
	fields := map[string]any{"channel_id": sel.Attempt.Instance, "attempt_id": sel.Attempt.AttemptID}
	if !finished {
		if teardownErr != nil {
			fields["error"] = teardownErr.Error()
		}
		logger.ErrorCF("agent", "Stopping the pairing did not finish; it stays stopping, no new pairing starts until it does, and the teardown is retried", fields)
		return false
	}
	logger.InfoCF("agent", "Stopped an in-flight channel pairing on Stop", fields)
	return true
}

// retryPairingTeardown retries a failed pairing teardown on that same
// attempt's own handle until it is disposed (T2-SF-14). It stops when the
// attempt is no longer the failed stopping one (another Stop retried it, or
// the connector was deleted); after the last delay a later Stop of its chat
// can still retry it.
func (al *AgentLoop) retryPairingTeardown(store *channels.PairingAttempts, attempt channels.PairingAttempt) {
	for _, delay := range pairingStopRetryDelays {
		time.Sleep(delay)
		sel, ok := store.BeginStop(attempt)
		if !ok {
			return
		}
		if al.finishPairingTeardown(store, sel, false) {
			return
		}
	}
	logger.ErrorCF("agent", "Stopping the pairing still has not finished; press Stop in its chat again, or restart the gateway",
		map[string]any{"channel_id": attempt.Instance, "attempt_id": attempt.AttemptID})
}
