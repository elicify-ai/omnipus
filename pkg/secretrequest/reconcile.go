// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package secretrequest

import (
	"errors"
	"os"
	"time"
)

// reconcilePendingLocked releases requests whose destinations no longer exist.
// It runs before counting capacity or accepting a submit: deleted sessions and
// agents never retain a slot forever. Unreadable state is not assumed deleted.
func (r *Registry) reconcilePendingLocked() error {
	if r.opts.Sessions == nil || r.opts.Config == nil {
		return errors.New("secret request service is unavailable")
	}
	cfg := r.opts.Config()
	if cfg == nil {
		return errors.New("live configuration is unavailable")
	}
	for id, rec := range r.entries {
		if rec.Card.State != "pending" {
			continue
		}
		meta, err := r.opts.Sessions.GetMeta(rec.Card.SessionId)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			delete(r.entries, id)
			delete(r.inFlight, id)
			rec.Card.State, rec.Card.UpdatedAt, rec.ReasonCode = "stopped", time.Now().UTC().Format(time.RFC3339Nano), "session_deleted"
			r.notifyLocked(rec, "stopped")
			continue
		}
		reason := ""
		if agentName(cfg, rec.Card.ActingAgentId) == "" {
			reason = "acting_agent_deleted"
		}
		if meta.AgentID != rec.Card.ActingAgentId {
			reason = "session_binding_changed"
		}
		if rec.Card.RootSessionId != nil {
			if _, rootErr := r.opts.Sessions.GetMeta(*rec.Card.RootSessionId); rootErr != nil {
				if !errors.Is(rootErr, os.ErrNotExist) {
					return rootErr
				}
				reason = "root_session_deleted"
			}
		}
		if reason == "" {
			continue
		}
		rec.ReasonCode = reason
		if _, finishErr := r.finishLocked(rec, "refused"); finishErr != nil {
			return finishErr
		}
	}
	return nil
}

// AuditRefusal records a value-free refusal without changing a valid pending
// card. No payload or credential text is accepted by this API.
func (r *Registry) AuditRefusal(id, owner, reasonCode string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.entries[id]
	if !ok || (rec.Owner != "" && rec.Owner != owner) {
		return
	}
	r.auditRefusalLocked(rec, reasonCode)
}

func (r *Registry) auditRefusalLocked(rec Record, reasonCode string) {
	rec.ReasonCode = reasonCode
	if r.opts.Audit != nil {
		r.opts.Audit(cloneRecord(rec), "refused")
	}
}

// AuditTurnRefusal covers a request refused before a card exists (for example
// channel-origin or unattended input); identities come only from turn context.
func (r *Registry) AuditTurnRefusal(sessionID, agentID, reasonCode string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := Record{ReasonCode: reasonCode}
	rec.Card.SessionId, rec.Card.ActingAgentId, rec.Card.State = sessionID, agentID, "refused"
	if r.opts.Audit != nil {
		r.opts.Audit(rec, "refused")
	}
}
