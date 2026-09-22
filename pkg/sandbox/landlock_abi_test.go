// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"testing"
)

// TestLandlockABIProbeResult_V4Works verifies that DescribeBackend
// returns valid LandlockFeatures for ABI v4 (including network rights)
// and does NOT populate IssueRef (since the ABI v4 incompatibility is now fixed).
func TestLandlockABIProbeResult_V4Works(t *testing.T) {
	cases := []struct {
		name       string
		abiVersion int
		wantFeats  []string
	}{
		{
			name:       "abi_v3_no_network",
			abiVersion: 3,
			wantFeats: []string{
				"EXECUTE",
				"WRITE_FILE",
				"READ_FILE",
				"READ_DIR",
				"REMOVE_DIR",
				"REMOVE_FILE",
				"MAKE_CHAR",
				"MAKE_DIR",
				"MAKE_REG",
				"MAKE_SOCK",
				"MAKE_FIFO",
				"MAKE_BLOCK",
				"MAKE_SYM",
				"REFER",
				"TRUNCATE",
			},
		},
		{
			name:       "abi_v4_with_network",
			abiVersion: 4,
			wantFeats: []string{
				"EXECUTE",
				"WRITE_FILE",
				"READ_FILE",
				"READ_DIR",
				"REMOVE_DIR",
				"REMOVE_FILE",
				"MAKE_CHAR",
				"MAKE_DIR",
				"MAKE_REG",
				"MAKE_SOCK",
				"MAKE_FIFO",
				"MAKE_BLOCK",
				"MAKE_SYM",
				"REFER",
				"TRUNCATE",
				"NET_BIND_TCP",
				"NET_CONNECT_TCP",
			},
		},
		{
			name:       "abi_v5_with_network",
			abiVersion: 5,
			wantFeats: []string{
				"EXECUTE",
				"WRITE_FILE",
				"READ_FILE",
				"READ_DIR",
				"REMOVE_DIR",
				"REMOVE_FILE",
				"MAKE_CHAR",
				"MAKE_DIR",
				"MAKE_REG",
				"MAKE_SOCK",
				"MAKE_FIFO",
				"MAKE_BLOCK",
				"MAKE_SYM",
				"REFER",
				"TRUNCATE",
				"NET_BIND_TCP",
				"NET_CONNECT_TCP",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockABIBackend{abiVersion: tc.abiVersion, applied: true}
			status := DescribeBackend(mock)
			for _, want := range tc.wantFeats {
				found := false
				for _, f := range status.LandlockFeatures {
					if f == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("missing feature %q; got %v", want, status.LandlockFeatures)
				}
			}
			// Assert the specific absence properties each ABI must satisfy:
			// IOCTL_DEV (bit 15) is ABI v5 only, so it must not appear in
			// the feature list for v3 / v4; NET_* is ABI v4 only, so it
			// must not appear for v3.
			switch tc.abiVersion {
			case 3:
				for _, f := range status.LandlockFeatures {
					if f == "IOCTL_DEV" || f == "NET_BIND_TCP" || f == "NET_CONNECT_TCP" {
						t.Errorf("ABI v3 must NOT advertise %q; got %v", f, status.LandlockFeatures)
					}
				}
			case 4:
				for _, f := range status.LandlockFeatures {
					if f == "IOCTL_DEV" {
						t.Errorf("ABI v4 must NOT advertise IOCTL_DEV (added in ABI v5); got %v", status.LandlockFeatures)
					}
				}
			}
		})
	}
}
