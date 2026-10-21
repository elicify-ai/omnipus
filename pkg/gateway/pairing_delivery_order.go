// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/agent"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

type pairingDeliveryStamp struct {
	generation uint64
	sequence   uint64
	attemptID  string
	status     channels.PairingStatus
}

func pairingStamp(p agent.WhatsAppPairingPayload) pairingDeliveryStamp {
	return pairingDeliveryStamp{p.Generation, p.Seq, p.AttemptID, p.Status}
}

func (s pairingDeliveryStamp) before(next pairingDeliveryStamp) bool {
	return s.generation < next.generation || s.generation == next.generation && s.sequence < next.sequence
}

// lockPairingDelivery enters the SAME manager-owned order as writers/native
// effects. No cache/interest/queue lock may be held by its caller. A handler
// without a manager has only the existing cache lock and no tracked resources.
func (h *WSHandler) lockPairingDelivery(channelID string) (*channels.PairingOrder, func()) {
	var order *channels.PairingOrder
	if h.agentLoop != nil {
		if mgr := h.agentLoop.GetChannelManager(); mgr != nil {
			order = mgr.PairingAttempts().LockChannel(channelID)
		}
	}
	h.pairingCacheMu.Lock()
	return order, func() {
		h.pairingCacheMu.Unlock()
		if order != nil {
			order.Unlock()
		}
	}
}

// pairingFreshLocked is not an unlocked permission. Its caller performs its
// cache/queue effect before releasing lockPairingDelivery's guard.
func (h *WSHandler) pairingFreshLocked(order *channels.PairingOrder, channelID string, stamp pairingDeliveryStamp) bool {
	if order != nil && !order.AcceptsProgress(stamp.generation, stamp.sequence, stamp.attemptID, stamp.status) {
		return false
	}
	last, exists := h.pairingCacheStamp[channelID]
	return !exists || !stamp.before(last)
}

func (h *WSHandler) enqueuePairing(wc *wsConn, p agent.WhatsAppPairingPayload, frame []byte) {
	order, unlock := h.lockPairingDelivery(p.ChannelID)
	defer unlock()
	if !h.pairingFreshLocked(order, p.ChannelID, pairingStamp(p)) || !wc.wantsPairing(p.ChannelID) {
		return
	}
	sendRawFrameBytes(wc, string(generated.WsFrameTypeWhatsappPairing), frame)
}

func (h *WSHandler) replayPairing(wc *wsConn, channelID string, active bool) {
	order, unlock := h.lockPairingDelivery(channelID)
	wc.setPairingInterest(channelID, active)
	cached, exists := h.lastPairingState.Load(channelID)
	stamp, stamped := h.pairingCacheStamp[channelID]
	if !active || !exists || order != nil && (!stamped || !h.pairingFreshLocked(order, channelID, stamp)) {
		unlock()
		return
	}
	frame, ok := cached.([]byte)
	unlock()
	if !ok {
		slog.Error("ws: lastPairingState held non-[]byte value, skipping re-emit", "channel_id", channelID)
		return
	}
	if len(frame) == 0 {
		return
	}
	// Passive speculative stage: a writer/new live event can win here. Never
	// retain bytes as permission; compare the selected stamp again at enqueue.
	runPairingDeliveryAfterCheckHook("replay", wc, channelID)
	order, unlock = h.lockPairingDelivery(channelID)
	defer unlock()
	current, hasCurrent := h.pairingCacheStamp[channelID]
	if stamped != hasCurrent || stamped && stamp != current || !h.pairingFreshLocked(order, channelID, stamp) || !wc.wantsPairing(channelID) {
		return
	}
	if _, stillCached := h.lastPairingState.Load(channelID); !stillCached {
		return // an intervening terminal event evicted these bytes
	}
	sendRawFrameBytes(wc, string(generated.WsFrameTypeWhatsappPairing), frame)
}

func (h *WSHandler) cachePairing(p agent.WhatsAppPairingPayload, frame generated.WhatsAppPairingFrame) {
	cache := p.Status == channels.PairingStatusCode
	switch p.Status {
	case channels.PairingStatusLinked, channels.PairingStatusTimeout, channels.PairingStatusError, channels.PairingStatusStopped:
		frame.Qr = nil
		cache = p.RequestID != "" || p.RestartInterrupted
	}
	// Marshal outside the ordering guard; there is no wire-format metadata.
	var data []byte
	if cache {
		var err error
		data, err = json.Marshal(frame)
		if err != nil {
			slog.Error("ws: failed to marshal whatsapp_pairing frame for cache", "channel_id", p.ChannelID, "error", err)
			return
		}
	}
	order, unlock := h.lockPairingDelivery(p.ChannelID)
	defer unlock()
	stamp := pairingStamp(p)
	if !h.pairingFreshLocked(order, p.ChannelID, stamp) {
		return
	}
	if h.pairingCacheStamp == nil {
		h.pairingCacheStamp = make(map[string]pairingDeliveryStamp)
	}
	h.pairingCacheStamp[p.ChannelID] = stamp
	if cache {
		h.lastPairingState.Store(p.ChannelID, data)
	} else {
		h.lastPairingState.Delete(p.ChannelID)
	}
}
