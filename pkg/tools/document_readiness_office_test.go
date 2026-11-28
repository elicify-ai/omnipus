// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Office-document preview — the bash readiness gate goes registry-free
// (office-preview spec AD-3, TDD row T-42, MV-4; BDD B-51).
//
// Oracle from the spec (AD-3): documentEnvTurn's readiness gate is
// "replaced by a registry-free readiness check (T42). This is the one
// intended change to bash composition" — the gate must stop consulting the
// converter registry store (whose deletion FR-025 then removes). MV-4:
// bash composition stays byte-identical except this AD-3 change.
//
// The healthy/missing/corrupt characterization the spec requires before
// the swap is carried by the existing production-runtime tests (see
// wu23_r1_production_runtime_darwin_test.go::documentEnvTurn); this file
// pins the end state.
//
// RED reason on the pre-change code: shell_env.go still resolves readiness
// through documentruntime.ConverterServiceOverride (verified by reading
// shell_env.go before writing).

import (
	"os"
	"strings"
	"testing"
)

// TestOfficePreview_ReadinessGateRegistryFree (T-42, B-51): bash readiness
// no longer validates a converter registry.
func TestOfficePreview_ReadinessGateRegistryFree(t *testing.T) {
	data, err := os.ReadFile("shell_env.go")
	if err != nil {
		t.Fatalf("reading shell_env.go failed: %v", err)
	}
	if strings.Contains(string(data), "ConverterServiceOverride") || strings.Contains(string(data), "ConverterServiceRegistry") {
		t.Fatalf("BLOCKED: documentEnvTurn still gates readiness through the converter registry — AD-3/T-42 replace it with a registry-free readiness check")
	}
}
