// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package secretrequest

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// persistLocked publishes the session state. Its error means the state was
// NOT published. Receipt cleanup for records retention just removed is
// maintenance after publication: a cleanup failure never un-publishes the
// state, it is logged and retried (retryReceiptMaintenanceLocked).
func (r *Registry) persistLocked(sessionID string) error {
	pruned, err := r.persistStateLocked(sessionID)
	if err != nil {
		return err
	}
	for _, id := range pruned {
		r.receiptRetry[id] = sessionID
	}
	r.retryReceiptMaintenanceLocked()
	return nil
}

// persistStateLocked publishes the bounded session state without deleting
// credential receipts. Recovery defers receipt cleanup until its full scan succeeds.
func (r *Registry) persistStateLocked(sessionID string) ([]string, error) {
	var pending, terminal []Record
	for _, rec := range r.entries {
		if rec.Card.SessionId != sessionID {
			continue
		}
		if rec.Card.State == "pending" {
			pending = append(pending, rec)
		} else {
			terminal = append(terminal, rec)
		}
	}
	sort.Slice(terminal, func(i, j int) bool {
		if terminal[i].Card.UpdatedAt == terminal[j].Card.UpdatedAt {
			return terminal[i].Card.RequestId < terminal[j].Card.RequestId
		}
		return terminal[i].Card.UpdatedAt < terminal[j].Card.UpdatedAt
	})
	var pruned []string
	if excess := len(terminal) - MaxTerminalPerSession; excess > 0 {
		for _, rec := range terminal[:excess] {
			pruned = append(pruned, rec.Card.RequestId)
		}
		terminal = terminal[excess:]
	}
	all := append(append([]Record{}, terminal...), pending...)
	raw, err := json.Marshal(all)
	if err != nil {
		return nil, err
	}
	if err := r.opts.Sessions.WriteSecretRequestState(sessionID, raw); err != nil {
		return nil, err
	}
	for _, id := range pruned {
		delete(r.entries, id)
		delete(r.inFlight, id)
	}
	return pruned, nil
}

// Recover runs once at boot, before the gateway accepts a card submit. Receipt
// publication wins over stale pending metadata. A pending card with no receipt
// becomes interrupted; it is NEVER re-offered as executable after restart.
func (r *Registry) Recover() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recovered {
		return r.initErr
	}
	r.recovered = true
	if r.opts.Sessions == nil {
		return nil
	}
	inventory, err := r.loadRequestInventoryLocked()
	if err != nil {
		r.initErr = recoveryFault(err)
		return r.initErr
	}
	for sessionID, records := range inventory {
		changed := false
		for _, rec := range records {
			if rec.Card.State == "pending" {
				recovered, recoverErr := r.recoverPendingLocked(rec)
				if recoverErr != nil {
					logStorageFault("recover_receipt", rec.Card.RequestId, sessionID, recoverErr, repairRecovery)
					r.initErr = recoveryFault(recoverErr)
					return r.initErr
				}
				rec, changed = recovered, true
			}
			r.entries[rec.Card.RequestId] = rec
		}
		if changed {
			if _, persistErr := r.persistStateLocked(sessionID); persistErr != nil {
				logStorageFault("recover_state_publication", "", sessionID, persistErr, repairRecovery)
				r.initErr = recoveryFault(persistErr)
				return r.initErr
			}
			for _, rec := range records {
				recovered := r.entries[rec.Card.RequestId]
				if recovered.Card.State == "interrupted" && r.opts.Audit != nil {
					r.opts.Audit(recovered, "interrupted")
				}
				// A receipt proves the values were committed before the crash
				// cut the normal submitted transition: record it truthfully.
				if rec.Card.State == "pending" && recovered.Card.State == "submitted" && r.opts.Audit != nil {
					r.opts.Audit(cloneRecord(recovered), "recovered_submitted")
				}
			}
		}
	}
	if sweepErr := r.pruneOrphanReceiptsLocked(); sweepErr != nil {
		logStorageFault("recover_receipt_sweep", "", "", sweepErr, repairRecovery)
		r.initErr = recoveryFault(sweepErr)
		return r.initErr
	}
	return nil
}

// recoveryFault is the value-free boot error: a fixed repair sentence whose
// typed cause stays reachable for errors.Is/As.
func recoveryFault(cause error) error {
	return &storageFaultError{message: "saved secret request state could not be recovered — " + repairRecovery, cause: cause}
}

// Snapshot contains only generated, value-free wire shapes visible to the
// account and (when supplied) the attached root or acting session.
func (r *Registry) Snapshot(owner, sessionID string) []gen.SessionStateSecretRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]gen.SessionStateSecretRequest, 0)
	for _, rec := range r.entries {
		if rec.Owner != "" && rec.Owner != owner {
			continue
		}
		if sessionID != "" && rec.Card.SessionId != sessionID && (rec.Card.RootSessionId == nil || *rec.Card.RootSessionId != sessionID) {
			continue
		}
		if _, err := r.opts.Sessions.GetMeta(rec.Card.SessionId); err != nil {
			continue
		}
		out = append(out, cloneRecord(rec).Card)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt == out[j].UpdatedAt {
			return out[i].RequestId < out[j].RequestId
		}
		return out[i].UpdatedAt < out[j].UpdatedAt
	})
	return out
}

// Stop resolves pending requests of the stopped session and every card it
// owns as a root. Submitted values and receipts are never rolled back.
func (r *Registry) Stop(sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var failures []error
	for _, rec := range r.entries {
		if rec.Card.SessionId != sessionID && (rec.Card.RootSessionId == nil || *rec.Card.RootSessionId != sessionID) {
			continue
		}
		if rec.Card.State == "pending" {
			if _, err := r.finishLocked(rec, "stopped"); err != nil {
				failures = append(failures, err)
			}
		} else if rec.Card.State == "submitted" && (!rec.ResumeStopped || r.vetoPending[rec.Card.RequestId]) {
			// The veto takes effect in memory at once; it counts as done only
			// once published. A failed publication stays pending and the
			// next Stop retries it (never a skip on the cached flag).
			rec.ResumeStopped = true
			// A deliberate Stop is not a failed delivery (M13).
			rec.Card.ContinuationUnconfirmed = nil
			r.entries[rec.Card.RequestId] = rec
			delete(r.inFlight, rec.Card.RequestId)
			if err := r.persistLocked(rec.Card.SessionId); err != nil {
				r.vetoPending[rec.Card.RequestId] = true
				failures = append(failures, err)
				continue
			}
			delete(r.vetoPending, rec.Card.RequestId)
			if r.opts.Audit != nil {
				rec.ReasonCode = "session_stopped"
				r.opts.Audit(cloneRecord(rec), "resume_stopped")
			}
		}
	}
	return errors.Join(failures...)
}

// ResumeMessage is intentionally reference-only. It is the sole resume
// payload producer used for initial dispatch, boot retry and consumption.
func ResumeMessage(rec Record) string {
	return fmt.Sprintf("Secret request %s was submitted. Stored reference names: %s. Use these references only for the recipient shown on the card; do not ask for or repeat secret values.", rec.Card.RequestId, strings.Join(rec.Card.ReferenceNames, ", "))
}

func (r *Registry) resumeLocked(rec Record) error {
	if rec.Card.State != "submitted" || rec.ResumeStopped || r.inFlight[rec.Card.RequestId] {
		return nil
	}
	entries, err := r.opts.Sessions.ReadTranscript(rec.Card.SessionId)
	if err != nil {
		return &continuationFaultError{stage: continuationStageTranscript, err: err}
	}
	for _, entry := range entries {
		if entry.ID == ResumeEntryID(rec.Card.RequestId) {
			return nil
		}
	}
	if r.opts.Resume == nil {
		return &continuationFaultError{stage: continuationStageDispatch, err: errors.New("secret continuation dispatcher is unavailable")}
	}
	if err := r.opts.Resume(cloneRecord(rec)); err != nil {
		return &continuationFaultError{stage: continuationStageDispatch, err: err}
	}
	r.inFlight[rec.Card.RequestId] = true
	return nil
}

// RetryResumes repairs dispatch failures on boot or attach, but does not
// recreate a resume already present in the transcript (FR-087).
func (r *Registry) RetryResumes() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var failures []error
	for _, rec := range r.entries {
		if rec.Card.State != "submitted" {
			continue
		}
		if r.opts.Stopped != nil {
			stopped, err := r.opts.Stopped(rec.Card.SessionId)
			if err != nil {
				// Whether the session was stopped is unknown, so delivery is
				// unconfirmed: a fresh client must warn, never show an
				// ordinary "Saved" (gate r2 T2-SF-02). A stopped veto is
				// never weakened — this branch never delivers.
				r.setUnconfirmedLocked(rec.Card.RequestId, !rec.ResumeStopped)
				logStorageFault("continuation_session_check", rec.Card.RequestId, rec.Card.SessionId, err, repairSessionStorage)
				failures = append(failures, err)
				continue
			}
			if stopped {
				continue
			}
		}
		if err := r.ensureDeliveryLocked(rec); err != nil {
			// The warning flag is set; the operator also gets the request,
			// the failed stage, a safe cause and the repair (gate r3 T2-SF-02).
			logContinuationRetryFault(rec, err)
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// Stages of a continuation delivery a retry can fail at, and the repair for a
// dispatch failure (a storage failure uses repairSessionStorage).
const (
	continuationStageTranscript = "continuation_transcript_read"
	continuationStageDispatch   = "continuation_dispatch"
	repairContinuationDispatch  = "the follow-up could not be handed to the agent; reopen the chat or restart the gateway to retry it, " +
		"and check the agent loop in the server log if it keeps failing"
)

// continuationFaultError names the stage a continuation delivery failed at. Its
// text is the cause's; only the stage and a safe category are ever logged.
type continuationFaultError struct {
	stage string
	err   error
}

func (e *continuationFaultError) Error() string { return e.err.Error() }
func (e *continuationFaultError) Unwrap() error { return e.err }

// logContinuationRetryFault writes one Error-level, value-free diagnostic for
// a failed continuation retry: request and session ids, stage, safe cause
// category and repair — never the raw error text.
func logContinuationRetryFault(rec Record, err error) {
	var fault *continuationFaultError
	if errors.As(err, &fault) && fault.stage == continuationStageDispatch {
		logger.ErrorCF("secretrequest", "Secret request follow-up failed at stage "+fault.stage+"; "+repairContinuationDispatch,
			map[string]any{
				"request_id": rec.Card.RequestId, "session_id": rec.Card.SessionId, "stage": fault.stage,
				"cause_category": "dispatch_failed", "repair": repairContinuationDispatch,
			})
		return
	}
	logStorageFault(continuationStageTranscript, rec.Card.RequestId, rec.Card.SessionId, err, repairSessionStorage)
}

// ensureDeliveryLocked runs the one ensure-delivery primitive (resumeLocked)
// and records its outcome on the card as continuation_unconfirmed (Train 2
// gate r1 M13; architect ruling arch-m13-ruling.md): set while a submitted,
// not-stopped request has no confirmed delivery, cleared once the dispatcher
// accepted it or its transcript entry exists. The flag is value-free.
func (r *Registry) ensureDeliveryLocked(rec Record) error {
	err := r.resumeLocked(rec)
	r.setUnconfirmedLocked(rec.Card.RequestId, err != nil && rec.Card.State == "submitted" && !rec.ResumeStopped)
	return err
}

// setUnconfirmedLocked sets or clears the card's continuation_unconfirmed
// flag (present only when true) and republishes the session's state when it
// changed, so a fresh tab and a restart read the same fact. A publication
// failure is logged value-free; the in-memory flag still serves snapshots.
func (r *Registry) setUnconfirmedLocked(id string, unconfirmed bool) {
	rec, ok := r.entries[id]
	if !ok {
		return
	}
	current := rec.Card.ContinuationUnconfirmed != nil && *rec.Card.ContinuationUnconfirmed
	if current == unconfirmed {
		return
	}
	if unconfirmed {
		flag := true
		rec.Card.ContinuationUnconfirmed = &flag
	} else {
		rec.Card.ContinuationUnconfirmed = nil
	}
	r.entries[id] = rec
	if err := r.persistLocked(rec.Card.SessionId); err != nil {
		logStorageFault("continuation_flag", id, rec.Card.SessionId, err, repairSessionStorage)
	}
}

func ResumeEntryID(id string) string { return "secret-resume-" + id }

// DeferResume releases transport acceptance when downstream worker admission
// rejected the message. The durable receipt remains the retry source; a full
// inbox must not leave an in-process "in flight" claim that can never consume.
func (r *Registry) DeferResume(id, sessionID, reasonCode string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.entries[id]
	if !ok || rec.Card.SessionId != sessionID || rec.Card.State != "submitted" {
		return false
	}
	delete(r.inFlight, id)
	rec.ReasonCode = reasonCode
	if r.opts.Audit != nil {
		r.opts.Audit(cloneRecord(rec), "resume_deferred")
	}
	// Acceptance was revoked downstream: not delivered (M13).
	r.setUnconfirmedLocked(id, !rec.ResumeStopped)
	return true
}

// ClaimResume is the serialized consumer boundary. A queued duplicate or a
// replay after restart never starts another turn for the same request id.
func (r *Registry) ClaimResume(id, sessionID string) (Record, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.entries[id]
	if !ok || rec.Card.State != "submitted" || rec.Card.SessionId != sessionID {
		return Record{}, false, errors.New("secret continuation is unavailable")
	}
	if rec.ResumeStopped {
		return Record{}, false, errors.New("secret continuation was explicitly stopped; saved references are unchanged")
	}
	if r.opts.Stopped != nil {
		stopped, err := r.opts.Stopped(sessionID)
		if err != nil || stopped {
			delete(r.inFlight, id)
			return Record{}, false, errors.New("secret continuation session is stopped or unavailable")
		}
		if rec.Card.RootSessionId != nil {
			rootStopped, rootErr := r.opts.Stopped(*rec.Card.RootSessionId)
			if rootErr != nil || rootStopped {
				delete(r.inFlight, id)
				return Record{}, false, errors.New("secret continuation root session is stopped or unavailable")
			}
		}
	}
	claimed, err := r.opts.Sessions.AppendTranscriptOnce(sessionID, session.TranscriptEntry{
		ID: ResumeEntryID(id), Role: "user", AgentID: rec.Card.ActingAgentId, Content: ResumeMessage(rec),
	})
	delete(r.inFlight, id)
	if err == nil {
		// The resume entry is in the transcript: delivery confirmed (M13).
		r.setUnconfirmedLocked(id, false)
		rec = r.entries[id]
	}
	return cloneRecord(rec), claimed, err
}
