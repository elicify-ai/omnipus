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
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func (r *Registry) persistLocked(sessionID string) error {
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
		return err
	}
	if err := r.opts.Sessions.WriteSecretRequestState(sessionID, raw); err != nil {
		return err
	}
	for _, id := range pruned {
		delete(r.entries, id)
		delete(r.inFlight, id)
	}
	if len(pruned) > 0 && r.opts.Credentials != nil {
		store, err := r.opts.Credentials()
		if err != nil {
			return err
		}
		return store.PruneCardReceipts(pruned)
	}
	return nil
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
	sessions, err := r.opts.Sessions.ListSessions()
	if err != nil {
		r.initErr = err
		return err
	}
	for _, meta := range sessions {
		raw, readErr := r.opts.Sessions.ReadSecretRequestState(meta.ID)
		if readErr != nil {
			r.initErr = readErr
			return readErr
		}
		var records []Record
		if err := json.Unmarshal(raw, &records); err != nil {
			r.initErr = err
			return err
		}
		changed := false
		for _, rec := range records {
			if rec.Card.SessionId != meta.ID || rec.Card.RequestId == "" || len(rec.Recipients) != len(rec.Card.Fields) {
				r.initErr = errors.New("invalid saved secret request metadata")
				return r.initErr
			}
			if rec.Card.State == "pending" {
				store, storeErr := r.opts.Credentials()
				if storeErr != nil {
					r.initErr = storeErr
					return storeErr
				}
				receipt, exists, receiptErr := store.CardReceipt(rec.Card.RequestId)
				if receiptErr != nil {
					r.initErr = receiptErr
					return receiptErr
				}
				if exists {
					if receipt.SessionID != rec.Card.SessionId {
						r.initErr = errors.New("secret receipt session mismatch")
						return r.initErr
					}
					rec.Card.State, rec.Card.ReferenceNames, rec.Card.UpdatedAt = "submitted", receipt.References, receipt.SubmittedAt.Format(time.RFC3339Nano)
				} else {
					rec.Card.State, rec.Card.UpdatedAt = "interrupted", time.Now().UTC().Format(time.RFC3339Nano)
				}
				changed = true
			}
			r.entries[rec.Card.RequestId] = rec
		}
		if changed {
			if err := r.persistLocked(meta.ID); err != nil {
				return err
			}
			for _, rec := range records {
				recovered := r.entries[rec.Card.RequestId]
				if recovered.Card.State == "interrupted" && r.opts.Audit != nil {
					r.opts.Audit(recovered, "interrupted")
				}
			}
		}
	}
	return nil
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
		if rec.Card.State != "pending" {
			continue
		}
		if rec.Card.SessionId == sessionID || (rec.Card.RootSessionId != nil && *rec.Card.RootSessionId == sessionID) {
			if _, err := r.finishLocked(rec, "stopped"); err != nil {
				failures = append(failures, err)
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
	if rec.Card.State != "submitted" || r.inFlight[rec.Card.RequestId] {
		return nil
	}
	entries, err := r.opts.Sessions.ReadTranscript(rec.Card.SessionId)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.ID == ResumeEntryID(rec.Card.RequestId) {
			return nil
		}
	}
	if r.opts.Resume == nil {
		return errors.New("secret continuation dispatcher is unavailable")
	}
	if err := r.opts.Resume(cloneRecord(rec)); err != nil {
		return err
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
				failures = append(failures, err)
				continue
			}
			if stopped {
				continue
			}
		}
		if err := r.resumeLocked(rec); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func ResumeEntryID(id string) string { return "secret-resume-" + id }

// ClaimResume is the serialized consumer boundary. A queued duplicate or a
// replay after restart never starts another turn for the same request id.
func (r *Registry) ClaimResume(id, sessionID string) (Record, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.entries[id]
	if !ok || rec.Card.State != "submitted" || rec.Card.SessionId != sessionID {
		return Record{}, false, errors.New("secret continuation is unavailable")
	}
	if r.opts.Stopped != nil {
		stopped, err := r.opts.Stopped(sessionID)
		if err != nil || stopped {
			delete(r.inFlight, id)
			return Record{}, false, errors.New("secret continuation session is stopped or unavailable")
		}
	}
	claimed, err := r.opts.Sessions.AppendTranscriptOnce(sessionID, session.TranscriptEntry{
		ID: ResumeEntryID(id), Role: "user", AgentID: rec.Card.ActingAgentId, Content: ResumeMessage(rec),
	})
	delete(r.inFlight, id)
	return cloneRecord(rec), claimed, err
}
