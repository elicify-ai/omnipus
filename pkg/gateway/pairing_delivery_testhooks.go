// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"sync"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// These passive stages let ordering tests hold real pairing delivery without
// replacing its cache, freshness decision or queue. Both callbacks are nil in
// production and have no return value or authority over delivery.
//
// Tests install/reset the callbacks under pairingDeliveryStageHookMu before
// starting participants and after joining them. Invocation releases that mutex
// before calling a hook, so a held stage does not hold the hook registry lock.
var (
	pairingDeliveryStageHookMu sync.RWMutex

	// pairingDeliveryAfterCheckHook runs after the speculative live freshness
	// check, or after replay selects a nonempty cached byte snapshot. Replay
	// currently has no additional freshness check; this seam does not add one.
	// path is "live" or "replay".
	pairingDeliveryAfterCheckHook func(path string, wc *wsConn, channelID string)

	// pairingDeliveryBeforeEnqueueHook runs immediately before the existing
	// raw queue append, for both live and replay pairing frames. frame is a
	// private copy: observing or changing it cannot alter the queued bytes.
	pairingDeliveryBeforeEnqueueHook func(wc *wsConn, frame []byte)
)

func runPairingDeliveryAfterCheckHook(path string, wc *wsConn, channelID string) {
	pairingDeliveryStageHookMu.RLock()
	hook := pairingDeliveryAfterCheckHook
	pairingDeliveryStageHookMu.RUnlock()
	if hook != nil {
		hook(path, wc, channelID)
	}
}

func runPairingDeliveryBeforeEnqueueHook(wc *wsConn, frameType string, frame []byte) {
	if frameType != string(generated.WsFrameTypeWhatsappPairing) {
		return
	}
	pairingDeliveryStageHookMu.RLock()
	hook := pairingDeliveryBeforeEnqueueHook
	pairingDeliveryStageHookMu.RUnlock()
	if hook != nil {
		hook(wc, append([]byte(nil), frame...))
	}
}
