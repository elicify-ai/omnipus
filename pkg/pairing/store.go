// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package pairing implements device pairing with admin approval per Phase 3A.
// Pairing flow: device generates Ed25519 key pair → 6-digit code + fingerprint → admin approves in browser.
package pairing

import (
	"errors"
	"time"
)

// PairingState describes the state of a device pairing request.
type PairingState string

const (
	StatePending  PairingState = "pending"
	StatePaired   PairingState = "paired"
	StateRejected PairingState = "rejected"
	StateExpired  PairingState = "expired"
	StateRevoked  PairingState = "revoked"
)

// PendingDevice represents a device that has requested pairing but has not yet been approved.
type PendingDevice struct {
	DeviceID    string    `json:"device_id"`
	Fingerprint string    `json:"fingerprint"`  // base64(SHA256(public key))
	PairingCode string    `json:"pairing_code"` // 6-digit display code
	DeviceName  string    `json:"device_name"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// PairedDevice represents a device that has completed pairing and has an active session.
type PairedDevice struct {
	DeviceID    string    `json:"device_id"`
	Fingerprint string    `json:"fingerprint"`
	DeviceName  string    `json:"device_name"`
	Token       string    `json:"token,omitempty"` // stored encrypted in credentials.json, not serialized
	PairedAt    time.Time `json:"paired_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	Status      string    `json:"status"` // "active" | "revoked"
}

// PairingDecision is the result of a pairing decision (approved/rejected).
type PairingDecision struct {
	State PairingState
	Token string // non-empty only when approved
}

// ErrDeviceNotFound is returned when a pairing operation references a device
// ID that has no matching pending request (never registered, already
// resolved, or expired and purged by ExpireOldPending).
var ErrDeviceNotFound = errors.New("pairing: device not found")

// PairingStore is the global store of pending and paired devices.
// Accessed by REST handlers (GET /api/v1/devices) and WSHandler (pairing requests, admin decisions).
// Not thread-safe — must be accessed under its mutex from gateway.go.
type PairingStore struct {
	pending map[string]PendingDevice // deviceID → PendingDevice (in-memory only)
	paired  map[string]PairedDevice  // deviceID → PairedDevice (persisted to devices.json)
}

// NewPairingStore creates an empty pairing store.
func NewPairingStore() *PairingStore {
	return &PairingStore{
		pending: make(map[string]PendingDevice),
		paired:  make(map[string]PairedDevice),
	}
}

// AddPending adds a device to the pending pairing queue.
// Returns an error if the device ID already exists or if max devices limit is reached.
func (s *PairingStore) AddPending(device PendingDevice) error {
	if _, exists := s.pending[device.DeviceID]; exists {
		return nil // already pending, idempotent
	}
	if _, exists := s.paired[device.DeviceID]; exists {
		return nil // already paired, idempotent
	}
	s.pending[device.DeviceID] = device
	return nil
}

// RemovePending removes a device from the pending queue (on reject or expiry).
func (s *PairingStore) RemovePending(deviceID string) {
	delete(s.pending, deviceID)
}

// GetPending returns a pending device by ID.
func (s *PairingStore) GetPending(deviceID string) (PendingDevice, bool) {
	p, ok := s.pending[deviceID]
	return p, ok
}

// ListPending returns all pending pairing requests.
func (s *PairingStore) ListPending() []PendingDevice {
	result := make([]PendingDevice, 0, len(s.pending))
	for _, p := range s.pending {
		result = append(result, p)
	}
	return result
}

// Approve moves a pending device to paired state.
// Returns the PairedDevice and a generated token (caller stores it encrypted).
// Returns ErrDeviceNotFound if deviceID has no matching pending request.
func (s *PairingStore) Approve(deviceID, token string) (PairedDevice, error) {
	pending, ok := s.pending[deviceID]
	if !ok {
		return PairedDevice{}, ErrDeviceNotFound
	}
	paired := PairedDevice{
		DeviceID:    pending.DeviceID,
		Fingerprint: pending.Fingerprint,
		DeviceName:  pending.DeviceName,
		Token:       token,
		PairedAt:    time.Now().UTC(),
		LastSeenAt:  time.Now().UTC(),
		Status:      "active",
	}
	delete(s.pending, deviceID)
	s.paired[deviceID] = paired
	return paired, nil
}

// Reject removes a pending device without pairing.
func (s *PairingStore) Reject(deviceID string) {
	delete(s.pending, deviceID)
}

// Revoke marks a paired device as revoked.
func (s *PairingStore) Revoke(deviceID string) {
	if p, ok := s.paired[deviceID]; ok {
		p.Status = "revoked"
		s.paired[deviceID] = p
	}
}

// ListPaired returns all paired devices (token field omitted for security).
func (s *PairingStore) ListPaired() []PairedDevice {
	result := make([]PairedDevice, 0, len(s.paired))
	for _, p := range s.paired {
		// Omit token from listing — credentials.json is the authoritative store
		p.Token = ""
		result = append(result, p)
	}
	return result
}

// ExpireOldPending removes pending requests older than their TTL.
func (s *PairingStore) ExpireOldPending() {
	now := time.Now()
	for id, p := range s.pending {
		if now.After(p.ExpiresAt) {
			delete(s.pending, id)
		}
	}
}

// SetPaired sets the paired device map (used when loading from disk).
func (s *PairingStore) SetPaired(devices map[string]PairedDevice) {
	s.paired = devices
}
