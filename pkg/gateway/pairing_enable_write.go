// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/channels"
)

var errChannelEnableRefused = errors.New("channel enable refused before save")

// saveChannelEnabled owns config-write -> channel-order -> exact local commit.
// No manager operation, event callback or reload wait runs inside these locks.
func (a *restAPI) saveChannelEnabled(w http.ResponseWriter, channelID, baseType string, enabled bool, opts channelEnableOptions) error {
	store := a.pairingOrderStore()
	a.configMu.Lock()
	var order *channels.PairingOrder
	var fixed []*channels.PairingOrder
	if store != nil {
		order = store.LockChannel(channelID)
		fixed = []*channels.PairingOrder{order}
	}
	reservations := make(map[string]string)
	err := a.orderedConfigJSONLocked(store, fixed, reservations, func(doc map[string]any) error {
		entries, _ := doc["channels"].(map[string]any)
		if entries == nil {
			entries = make(map[string]any)
			doc["channels"] = entries
		}
		entry, _ := entries[channelID].(map[string]any)
		if entry == nil {
			if opts.requireExisting {
				return errChannelAbsent
			}
			entry = make(map[string]any)
			entries[channelID] = entry
		}
		if enabled {
			if msg := validateChannelConfigComplete(baseType, entry, nil, nil); msg != "" {
				jsonErr(w, http.StatusBadRequest, msg)
				return errChannelEnableRefused
			}
			var attemptID string
			var ok bool
			if opts.retryRequest != nil {
				attemptID, ok = a.admitPairingRetry(w, opts.retryRequest, channelID, opts.requestID, opts.sessionID, order)
			} else {
				alreadyEnabled, _ := entry["enabled"].(bool)
				attemptID, ok = a.admitOrdinaryPairing(w, channelID, order, alreadyEnabled)
			}
			if !ok {
				return errChannelEnableRefused
			}
			if attemptID != "" {
				reservations[channelID] = attemptID
				if opts.attemptID != nil {
					*opts.attemptID = attemptID
				}
			}
		}
		entry["type"], entry["enabled"] = baseType, enabled
		return nil
	})
	if err != nil && order != nil {
		// A pre-save failure releases only this operation's own reservation.
		// A saved/not-applied reservation was already interrupted at commit.
		if attemptID := reservations[channelID]; attemptID != "" {
			order.Release(attemptID, channels.PairingOutcomeInterrupted)
		}
	}
	if order != nil {
		order.Unlock()
	}
	a.configMu.Unlock()
	return err
}
