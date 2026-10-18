// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package secretrequest

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// ErrRecoveryStoreAbsent is the benign fresh-install case. Recovery must
// distinguish an absent store from an unreadable one, without creating a key.
var ErrRecoveryStoreAbsent = errors.New("secret recovery: no credential store exists yet")

// loadRequestInventoryLocked must complete before any receipt is deleted.
// ListSessions deliberately omits unreadable core metadata; that is unknown
// state, not evidence that the omitted request's receipt became an orphan.
func (r *Registry) loadRequestInventoryLocked() (map[string][]Record, error) {
	sessions, err := r.opts.Sessions.ListSessions()
	if err != nil {
		return nil, err
	}
	unreadable, err := r.opts.Sessions.UnreadableSessionIDs()
	if err != nil {
		return nil, err
	}
	if len(unreadable) != 0 {
		for _, id := range unreadable {
			logStorageFault("recover_inventory", "", id, errInvalidRequestMetadata, repairRecovery)
		}
		return nil, errors.New("secret recovery refused: some session metadata is unreadable")
	}
	inventory := make(map[string][]Record, len(sessions))
	seen := make(map[string]bool)
	for _, meta := range sessions {
		raw, readErr := r.opts.Sessions.ReadSecretRequestState(meta.ID)
		if readErr != nil {
			logStorageFault("recover_inventory", "", meta.ID, readErr, repairRecovery)
			return nil, readErr
		}
		var records []Record
		if decodeErr := json.Unmarshal(raw, &records); decodeErr != nil {
			logStorageFault("recover_inventory", "", meta.ID, decodeErr, repairRecovery)
			return nil, decodeErr
		}
		for _, rec := range records {
			if rec.Card.SessionId != meta.ID || rec.Card.RequestId == "" || seen[rec.Card.RequestId] || len(rec.Recipients) != len(rec.Card.Fields) {
				logStorageFault("recover_inventory", rec.Card.RequestId, meta.ID, errInvalidRequestMetadata, repairRecovery)
				return nil, errInvalidRequestMetadata
			}
			seen[rec.Card.RequestId] = true
		}
		inventory[meta.ID] = records
	}
	return inventory, nil
}

func (r *Registry) recoveryStoreLocked() (*credentials.Store, error) {
	if r.opts.RecoveryCredentials != nil {
		return r.opts.RecoveryCredentials()
	}
	if r.opts.Credentials == nil {
		return nil, ErrRecoveryStoreAbsent
	}
	return r.opts.Credentials()
}

func (r *Registry) recoverPendingLocked(rec Record) (Record, error) {
	store, err := r.recoveryStoreLocked()
	if err != nil && !errors.Is(err, ErrRecoveryStoreAbsent) {
		return rec, err
	}
	var receipt credentials.CardReceipt
	var exists bool
	if store != nil {
		receipt, exists, err = store.CardReceipt(rec.Card.RequestId)
		if err != nil {
			return rec, err
		}
	}
	if exists {
		if receipt.SessionID != rec.Card.SessionId {
			return rec, errors.New("secret receipt session mismatch")
		}
		rec.Card.State, rec.Card.ReferenceNames = "submitted", receipt.References
		rec.Card.UpdatedAt = receipt.SubmittedAt.Format(time.RFC3339Nano)
	} else {
		rec.Card.State, rec.Card.UpdatedAt = "interrupted", time.Now().UTC().Format(time.RFC3339Nano)
	}
	return rec, nil
}

func (r *Registry) pruneOrphanReceiptsLocked() error {
	store, err := r.recoveryStoreLocked()
	if errors.Is(err, ErrRecoveryStoreAbsent) {
		return nil
	}
	if err != nil {
		return err
	}
	if store == nil {
		return errors.New("secret recovery credential store is unavailable")
	}
	keep := make(map[string]string, len(r.entries))
	for id, rec := range r.entries {
		keep[id] = rec.Card.SessionId
	}
	return store.PruneOrphanCardReceipts(keep)
}
