// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package daemon

import "testing"

// TestProcessImageName_DeadPID proves the Windows process API reports a PID no
// host can allocate as dead rather than treating an OpenProcess failure as an
// unknown identity.
func TestProcessImageName_DeadPID(t *testing.T) {
	alive, _, err := processImageName(2147483647)
	if alive || err != nil {
		t.Fatalf("processImageName(dead): alive=%v err=%v, want false,nil", alive, err)
	}
}
