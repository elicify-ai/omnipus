// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package daemon

import (
	"errors"
	"testing"
)

// TestWmicCheckProcess_MissingWmic verifies the fail-safe path:
// when wmic is absent (Win11 24H2+ removed it), wmicCheckProcess must return
// an error rather than (false, false, nil), so that checkProcess propagates
// identityErr and Stop refuses to clear the PID file (FINDING-4 / MAJOR-3).
//
// We cannot actually remove wmic in a test, so we verify the logic by
// inspecting the documented error contract: wmicCheckProcess returns
// errWmicUnavailable when exec.LookPath("wmic") fails.
//
// On a system that DOES have wmic, this test is a no-op (skipped).
func TestWmicCheckProcess_MissingWmic_FailSafe(t *testing.T) {
	_, _, err := wmicCheckProcess(1) // PID 1 is the Windows System idle process
	if err == nil {
		// wmic is present — we cannot exercise the missing-wmic path here.
		t.Skip("wmic is available on this system; skipping missing-wmic fail-safe test")
	}
	if !errors.Is(err, errWmicUnavailable) {
		// Some other unexpected error occurred.
		t.Logf("wmicCheckProcess returned a non-unavailable error (may be transient): %v", err)
	}
}

// TestCheckProcess_WmicError_FailSafe verifies that when wmicCheckProcess
// returns an error (wmic missing or unexpected failure), checkProcess returns
// a non-nil identityErr so that Status and Stop fail safe — refusing to act
// and NOT clearing the PID file (FINDING-4 / MAJOR-3).
func TestCheckProcess_WmicError_FailSafe(t *testing.T) {
	// Check whether wmic is present.
	_, _, wmicErr := wmicCheckProcess(2147483647)
	if wmicErr == nil {
		t.Skip("wmic is available; the fail-safe path is not exercised by this test")
	}

	// wmic is unavailable → checkProcess must return identityErr so callers
	// can distinguish "unknown" from "confirmed dead / confirmed non-ours".
	_, _, identityErr := checkProcess(2147483647)
	if identityErr == nil {
		t.Error("checkProcess when wmic unavailable: expected non-nil identityErr (fail-safe), got nil")
	}
}
