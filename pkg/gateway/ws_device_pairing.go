//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// WebSocket handler and device pairing flow.

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"sync"

	"github.com/dapicom-ai/omnipus/pkg/pairing"
)

// devicePairingRegistry tracks in-flight pairing requests awaiting admin approval.
// Mirrors the wsApprovalRegistry pattern for tool approvals.
type devicePairingRegistry struct {
	mu      sync.Mutex
	pending map[string]chan pairing.PairingDecision
}

func newDevicePairingRegistry() *devicePairingRegistry {
	return &devicePairingRegistry{
		pending: make(map[string]chan pairing.PairingDecision),
	}
}

func (r *devicePairingRegistry) resolve(id string, decision pairing.PairingDecision) bool {
	r.mu.Lock()
	ch, ok := r.pending[id]
	r.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- decision:
		return true
	default:
		slog.Warn("ws: device pairing registry: duplicate resolve", "id", id)
		return false
	}
}

// handleDevicePairingResponse processes an admin's decision on a pending pairing request.
// Called from the readLoop when a "device_pairing_response" frame is received.
func (h *WSHandler) handleDevicePairingResponse(deviceID, decision string) {
	if deviceID == "" {
		slog.Warn("ws: device_pairing_response missing device_id")
		return
	}

	pairingStore := h.pairingStore
	if pairingStore == nil {
		slog.Error("ws: pairing store not initialized")
		return
	}

	pending, exists := pairingStore.GetPending(deviceID)
	if !exists {
		slog.Warn("ws: device_pairing_response for unknown device_id", "device_id", deviceID)
		return
	}

	switch decision {
	case "approve":
		// Generate a device token (32 bytes random, hex encoded).
		token, err := generateDeviceToken()
		if err != nil {
			slog.Error("ws: failed to generate device token", "device_id", deviceID, "error", err)
			return
		}
		paired, err := pairingStore.Approve(deviceID, token)
		if err != nil || paired.DeviceID == "" {
			slog.Error("ws: failed to approve device pairing", "device_id", deviceID, "error", err)
			return
		}
		slog.Info("ws: device pairing approved", "device_id", deviceID)

		// Notify the waiting goroutine (if any) on the registry channel.
		h.devicePairingRegistry.resolve(deviceID, pairing.PairingDecision{State: pairing.StatePaired, Token: token})

	case "reject":
		pairingStore.Reject(deviceID)
		slog.Info("ws: device pairing rejected",
			"device_id", deviceID,
			"device_name", pending.DeviceName,
		)

		h.devicePairingRegistry.resolve(deviceID, pairing.PairingDecision{State: pairing.StateRejected})

	default:
		slog.Warn("ws: unknown device_pairing_response decision", "decision", decision)
		return
	}
}

// generateDeviceToken creates a random device token using crypto/rand.
func generateDeviceToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
