// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package daemon

import (
	"errors"
	"os/exec"
	"testing"
)

// TestWmicCheckProcess_MissingWmic verifies the fail-safe path:
// when wmic is absent (Win11 24H2+ removed it), wmicCheckProcess must return
// an error rather than (false, false, nil), so that checkProcess propagates
// identityErr and Stop refuses to clear the PID file (FINDING-4 / MAJOR-3).
//
// The environment guard probes the PATH directly (not wmicCheckProcess, whose
// error contract is the behaviour under test): on a system that DOES have
// wmic, the missing-wmic path cannot be exercised and the test skips. On a
// system without wmic, anything other than errWmicUnavailable is a failure.
func TestWmicCheckProcess_MissingWmic_FailSafe(t *testing.T) {
	if _, lookErr := exec.LookPath("wmic"); lookErr == nil {
		t.Skip("wmic is available on this system; cannot exercise the missing-wmic path")
	}

	_, _, err := wmicCheckProcess(1) // PID 1 is the Windows System idle process
	if !errors.Is(err, errWmicUnavailable) {
		t.Errorf("wmicCheckProcess with wmic absent: expected errWmicUnavailable, got %v", err)
	}
}

// TestCheckProcess_WmicError_FailSafe verifies that when wmic is missing,
// checkProcess returns a non-nil identityErr so that Status and Stop fail
// safe — refusing to act and NOT clearing the PID file (FINDING-4 / MAJOR-3).
//
// The environment guard probes the PATH directly (not wmicCheckProcess, whose
// error contract feeds the behaviour under test), so a regression in either
// function surfaces as a failure, never as a skip.
func TestCheckProcess_WmicError_FailSafe(t *testing.T) {
	if _, lookErr := exec.LookPath("wmic"); lookErr == nil {
		t.Skip("wmic is available; the wmic-unavailable fail-safe path is not exercised by this test")
	}

	// wmic is unavailable → checkProcess must return identityErr so callers
	// can distinguish "unknown" from "confirmed dead / confirmed non-ours".
	_, _, identityErr := checkProcess(2147483647)
	if identityErr == nil {
		t.Error("checkProcess when wmic unavailable: expected non-nil identityErr (fail-safe), got nil")
	}
}
