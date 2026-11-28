// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package documentruntime

// Office-document preview — the document runtime loses its converter
// contract (office-preview spec AD-3, §6 "Document runtime soffice
// contract", FR-047/FR-025; TDD rows T-42/T-73; BDD B-51).
//
// Oracles from the spec (AD-3, binding), never from the code:
//   - ProvisionFirstParty "drops its `Converter: <prefix>/bin/soffice`
//     contract and the probe stops requiring soffice (agents never run
//     LibreOffice)".
//   - DocumentSkillIDs shrinks to the one merged office skill (AD-14:
//     "DocumentSkillIDs / InstallEmbeddedSkills" wiring).
//   - "The runtime registry store (ConverterServiceRegistryDir,
//     ConverterServiceOverride, PrepareConverterServiceRegistry,
//     MaterializeConverterServiceRegistry and helpers) is deleted only
//     after pkg/tools/shell_env.go::documentEnvTurn's readiness gate is
//     characterized (healthy/missing/corrupt) and replaced by a
//     registry-free readiness check (T-42)."
//
// RED reasons on the pre-change code (verified by reading provision.go,
// runtime.go and converter_registry.go before writing): all three still
// carry the converter contract.

import (
	"os"
	"strings"
	"testing"
)

// TestOfficePreview_ProvisionDropsSofficeContract (AD-3, FR-047, T-73,
// B-51): the provisioner must no longer name or demand soffice anywhere —
// agents never run LibreOffice.
func TestOfficePreview_ProvisionDropsSofficeContract(t *testing.T) {
	data, err := os.ReadFile("provision.go")
	if err != nil {
		t.Fatalf("reading provision.go failed: %v", err)
	}
	if strings.Contains(string(data), "soffice") {
		t.Fatalf("BLOCKED: provision.go still carries the soffice converter contract — AD-3 drops `Converter: <prefix>/bin/soffice` outright (T-73, FR-047, B-51)")
	}
}

// TestOfficePreview_SingleOfficeSkillID (AD-14, T-73): the runtime's skill
// wiring addresses exactly the merged office skill.
func TestOfficePreview_SingleOfficeSkillID(t *testing.T) {
	if len(DocumentSkillIDs) != 1 || DocumentSkillIDs[0] != "elicify-office" {
		t.Fatalf("BLOCKED: DocumentSkillIDs = %v, want [elicify-office] — AD-14 merges the four document skills (T-27/T-73)", DocumentSkillIDs)
	}
}

// TestOfficePreview_RegistryStoreDeletedAfterReadinessReplacement (AD-3,
// FR-025, T-42): the registry store is deleted once the readiness gate no
// longer needs it. RED: both the store and its use in the gate still exist
// (the deletion is sequenced AFTER the registry-free readiness check —
// this test enforces the end state).
func TestOfficePreview_RegistryStoreDeletedAfterReadinessReplacement(t *testing.T) {
	for _, sym := range []string{
		"ConverterServiceRegistryDir", "ConverterServiceOverride",
		"PrepareConverterServiceRegistry", "MaterializeConverterServiceRegistry",
	} {
		data, err := os.ReadFile("converter_registry.go")
		if err != nil {
			t.Fatalf("reading converter_registry.go failed: %v", err)
		}
		if strings.Contains(string(data), sym) {
			t.Fatalf("BLOCKED: converter_registry.go still defines %s — AD-3/FR-025 delete the registry store once documentEnvTurn's registry-free readiness check replaces the gate (T-42)", sym)
		}
	}
}
