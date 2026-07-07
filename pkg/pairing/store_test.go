// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package pairing

// store_test.go — tests for the device-pairing store (pkg/pairing/store.go).
//
// Covers the Approve happy path, the not-found error path (previously a
// silent zero-value-with-nil-error footgun — see ErrDeviceNotFound), and the
// interaction between ExpireOldPending and Approve for an expired pending
// request.
//
// Build tags: goolm,stdjson (CGO_ENABLED=0).
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run '.' -p 1 ./pkg/pairing/

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Approve — success
// ---------------------------------------------------------------------------

func TestApprove_Success(t *testing.T) {
	s := NewPairingStore()

	pending := PendingDevice{
		DeviceID:    "dev-1",
		Fingerprint: "fp-abc",
		PairingCode: "123456",
		DeviceName:  "Test Phone",
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(5 * time.Minute),
	}
	require.NoError(t, s.AddPending(pending))

	paired, err := s.Approve("dev-1", "tok-xyz")
	require.NoError(t, err)

	require.Equal(t, "dev-1", paired.DeviceID)
	require.Equal(t, "fp-abc", paired.Fingerprint)
	require.Equal(t, "Test Phone", paired.DeviceName)
	require.Equal(t, "tok-xyz", paired.Token)
	require.Equal(t, "active", paired.Status)
	require.False(t, paired.PairedAt.IsZero())
	require.False(t, paired.LastSeenAt.IsZero())

	// The device must have moved out of pending and into paired.
	_, stillPending := s.GetPending("dev-1")
	require.False(t, stillPending, "device should no longer be pending after approval")

	listed := s.ListPaired()
	require.Len(t, listed, 1)
	require.Equal(t, "dev-1", listed[0].DeviceID)
	// ListPaired must omit the token even though Approve's return value carries it.
	require.Empty(t, listed[0].Token)
}

// ---------------------------------------------------------------------------
// Approve — not found
// ---------------------------------------------------------------------------

func TestApprove_NotFound(t *testing.T) {
	s := NewPairingStore()

	paired, err := s.Approve("does-not-exist", "tok-xyz")

	require.Error(t, err, "Approve on an unknown device ID must return a non-nil error")
	require.True(t, errors.Is(err, ErrDeviceNotFound))
	require.Equal(t, PairedDevice{}, paired, "Approve must return the zero value alongside the error")

	// No device should have been created as a side effect.
	require.Empty(t, s.ListPaired())
}

// TestApprove_NotFound_DoesNotResurrectRejected confirms that once a device
// has been rejected (and thus removed from pending), approving it later
// still surfaces ErrDeviceNotFound rather than silently succeeding.
func TestApprove_NotFound_DoesNotResurrectRejected(t *testing.T) {
	s := NewPairingStore()
	require.NoError(t, s.AddPending(PendingDevice{
		DeviceID:  "dev-2",
		ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
	}))

	s.Reject("dev-2")

	paired, err := s.Approve("dev-2", "tok-2")
	require.ErrorIs(t, err, ErrDeviceNotFound)
	require.Equal(t, PairedDevice{}, paired)
}

// ---------------------------------------------------------------------------
// Pairing expiry
// ---------------------------------------------------------------------------

// TestExpireOldPending_RemovesExpiredEntries verifies ExpireOldPending purges
// only requests whose ExpiresAt has passed, leaving unexpired requests intact.
func TestExpireOldPending_RemovesExpiredEntries(t *testing.T) {
	s := NewPairingStore()

	require.NoError(t, s.AddPending(PendingDevice{
		DeviceID:  "expired-device",
		ExpiresAt: time.Now().UTC().Add(-1 * time.Minute), // already expired
	}))
	require.NoError(t, s.AddPending(PendingDevice{
		DeviceID:  "fresh-device",
		ExpiresAt: time.Now().UTC().Add(5 * time.Minute), // still valid
	}))

	s.ExpireOldPending()

	_, expiredStillPending := s.GetPending("expired-device")
	require.False(t, expiredStillPending, "expired pending request must be purged")

	_, freshStillPending := s.GetPending("fresh-device")
	require.True(t, freshStillPending, "unexpired pending request must survive")
}

// TestExpireOldPending_ExpiredDeviceNotApprovable proves the end-to-end
// consequence of expiry: once ExpireOldPending has run, an admin approving
// an expired pending request gets ErrDeviceNotFound instead of a fabricated
// success, since the pending entry no longer exists.
func TestExpireOldPending_ExpiredDeviceNotApprovable(t *testing.T) {
	s := NewPairingStore()

	require.NoError(t, s.AddPending(PendingDevice{
		DeviceID:  "expired-device",
		ExpiresAt: time.Now().UTC().Add(-1 * time.Minute),
	}))

	s.ExpireOldPending()

	paired, err := s.Approve("expired-device", "tok-expired")
	require.ErrorIs(t, err, ErrDeviceNotFound)
	require.Equal(t, PairedDevice{}, paired)
	require.Empty(t, s.ListPaired())
}
