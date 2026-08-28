// Omnipus — F4 reproduction tests for ToolSearch full-tier policy gate (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// F4: full-tier tools (ManifestFull) return "already available — just call it
// directly" WITHOUT checking policy. So a policy-DENIED full-tier tool falsely
// reports it is available.
//
// Fix required: in execLoad, when a name is ManifestFull/Infra, first verify
// it is in the agent's policy-filtered callable set before saying it's available.
// If it is policy-denied → return an error with the denial reason.

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestCanLoad_PolicyDeniedFullTierToolRejected proves that a DENIED full-tier
// tool (read_file is ManifestFull) returns a denial reason rather than
// "already available — just call it directly".
//
// Background: a canLoad callback that returns (false, "denied by policy") for
// full-tier names is what the agent loop wires when the agent's policy denies
// the tool. Before the fix, execLoad short-circuits for ManifestFull without
// calling canLoad, so the denial is never surfaced.
func TestCanLoad_PolicyDeniedFullTierToolRejected(t *testing.T) {
	// read_file is a canonical full-tier (ManifestFull) tool.
	if ToolManifestTier("read_file") != ManifestFull {
		t.Skip("read_file must be ManifestFull for this test to be valid")
	}

	// canLoad that denies read_file (simulating a policy-denied full-tier tool).
	deniedCanLoad := func(_ context.Context, name string) (bool, string) {
		if name == "read_file" {
			return false, "read_file — denied by this agent's policy"
		}
		return true, ""
	}
	markLoaded := func(_ context.Context, names []string) (map[string]any, []string) {
		return map[string]any{}, nil
	}

	tt := NewToolsTool(nil, 5, 10)
	tt.SetResolver(deniedCanLoad, markLoaded)

	r := tt.Execute(context.Background(), map[string]any{
		"names": []any{"read_file"},
	})

	// MUST be an error (denied by policy), NOT "already available".
	if !r.IsError {
		t.Errorf("F4: expected error for denied full-tier tool, got success: %s", r.ForLLM)
	}
	if strings.Contains(r.ForLLM, "already available") {
		t.Errorf("F4: policy-denied full-tier tool must NOT report 'already available'; got: %s", r.ForLLM)
	}
	if !strings.Contains(r.ForLLM, "denied") {
		t.Errorf("F4: error for denied full-tier tool must mention 'denied'; got: %s", r.ForLLM)
	}
}

// TestCanLoad_AllowedFullTierToolStillReportsAvailable proves that a full-tier
// tool that is NOT denied by policy still gets the "already available" response
// (i.e., the fix only gates on denial, doesn't break the happy path).
func TestCanLoad_AllowedFullTierToolStillReportsAvailable(t *testing.T) {
	if ToolManifestTier("read_file") != ManifestFull {
		t.Skip("read_file must be ManifestFull for this test to be valid")
	}

	// canLoad that allows read_file.
	allowedCanLoad := func(_ context.Context, name string) (bool, string) {
		return true, ""
	}
	markLoaded := func(_ context.Context, names []string) (map[string]any, []string) {
		return map[string]any{}, nil
	}

	tt := NewToolsTool(nil, 5, 10)
	tt.SetResolver(allowedCanLoad, markLoaded)

	r := tt.Execute(context.Background(), map[string]any{
		"names": []any{"read_file"},
	})

	// Must succeed with "already available" message (no-op success).
	if r.IsError {
		t.Errorf("F4: allowed full-tier tool must not be an error: %s", r.ForLLM)
	}
	if !strings.Contains(r.ForLLM, "already available") {
		t.Errorf("F4: allowed full-tier tool must say 'already available'; got: %s", r.ForLLM)
	}
}

// TestCanLoad_DeniedInfraTool proves the same fix applies to infra-tier tools.
func TestCanLoad_DeniedInfraTool(t *testing.T) {
	if ToolManifestTier("ToolSearch") != ManifestInfra {
		t.Skip("ToolSearch must be ManifestInfra for this test to be valid")
	}

	// canLoad that denies ToolSearch itself (extreme edge case, but must behave correctly).
	deniedCanLoad := func(_ context.Context, name string) (bool, string) {
		return false, name + " — denied"
	}
	markLoaded := func(_ context.Context, names []string) (map[string]any, []string) {
		return map[string]any{}, nil
	}

	tt := NewToolsTool(nil, 5, 10)
	tt.SetResolver(deniedCanLoad, markLoaded)

	r := tt.Execute(context.Background(), map[string]any{
		"names": []any{"ToolSearch"},
	})

	// MUST be an error, NOT "already available".
	if !r.IsError {
		t.Errorf("F4/infra: expected error for denied infra tool, got success: %s", r.ForLLM)
	}
	if strings.Contains(r.ForLLM, "already available") {
		t.Errorf("F4/infra: denied infra tool must NOT say 'already available'; got: %s", r.ForLLM)
	}
}

// ── Fix B: mixed full-tier-denied + lazy-allowed batch surfaces the denial ────

// TestExecLoad_MixedBatch_DenialNotSilenced reproduces Fix B (N1):
// A ToolSearch{names:[denied-full, allowed-lazy, allowed-full]} request must:
//   - Load the allowed-lazy tool (schemas present).
//   - List the denied-full tool in rejected with the policy reason.
//   - Acknowledge the allowed-full tool as already-available.
//   - NOT silently drop any of the three entries.
//
// Before the fix: fullTierRejections were only surfaced inside the
// `if len(lazyNames)==0` branch, so a mixed batch silently ignored denials.
func TestExecLoad_MixedBatch_DenialNotSilenced(t *testing.T) {
	const (
		// We need a ManifestFull name. read_file is canonical full-tier.
		deniedFull  = "read_file"
		allowedFull = "list_directory" // also ManifestFull
		allowedLazy = "find_skills"    // ManifestLazy
	)

	// Guard: tier assumptions must hold for the test to be meaningful.
	if ToolManifestTier(deniedFull) != ManifestFull {
		t.Skipf("%s must be ManifestFull", deniedFull)
	}
	if ToolManifestTier(allowedFull) != ManifestFull {
		t.Skipf("%s must be ManifestFull", allowedFull)
	}
	if ToolManifestTier(allowedLazy) != ManifestLazy {
		t.Skipf("%s must be ManifestLazy", allowedLazy)
	}

	// canLoad:
	//   - deniedFull → (false, "denied by policy") — denied at the full-tier gate
	//   - allowedFull → (false, CanLoadAlreadyAvailablePrefix+"…") — no-op hint
	//   - allowedLazy → (true, "") — lazy tool, load it
	canLoad := func(_ context.Context, name string) (bool, string) {
		switch name {
		case deniedFull:
			return false, name + " — denied by this agent's policy"
		case allowedFull:
			return false, CanLoadAlreadyAvailablePrefix + " — just call it directly"
		case allowedLazy:
			return true, ""
		default:
			return false, name + " — unknown"
		}
	}
	markLoaded := func(_ context.Context, names []string) (map[string]any, []string) {
		schemas := make(map[string]any, len(names))
		for _, n := range names {
			schemas[n] = map[string]any{"name": n}
		}
		return schemas, nil
	}

	tt := NewToolsTool(nil, 5, 10)
	tt.SetResolver(canLoad, markLoaded)

	r := tt.Execute(context.Background(), map[string]any{
		"names": []any{deniedFull, allowedLazy, allowedFull},
	})

	// Must NOT be an error overall (lazy tool loaded successfully).
	if r.IsError {
		t.Fatalf("MixedBatch: expected success (lazy tool loaded), got error: %s", r.ForLLM)
	}

	// Parse response.
	var result map[string]any
	if err := json.Unmarshal([]byte(r.ForLLM), &result); err != nil {
		t.Fatalf("MixedBatch: failed to parse response JSON: %v\nraw: %s", err, r.ForLLM)
	}

	// allowedLazy must appear in "loaded".
	loadedRaw, _ := result["loaded"].([]any)
	loadedSet := make(map[string]bool, len(loadedRaw))
	for _, v := range loadedRaw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("MixedBatch: unexpected type %T in loaded, want string", v)
		}
		loadedSet[s] = true
	}
	if !loadedSet[allowedLazy] {
		t.Errorf("MixedBatch: %s (allowed lazy) must be in loaded; got loaded=%v", allowedLazy, loadedRaw)
	}

	// deniedFull must appear in "rejected" with the denial reason.
	rejectedRaw, _ := result["rejected"].([]any)
	foundDenied := false
	for _, v := range rejectedRaw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("MixedBatch: unexpected type %T in rejected, want string", v)
		}
		if strings.Contains(s, deniedFull) && strings.Contains(s, "denied") {
			foundDenied = true
		}
	}
	if !foundDenied {
		t.Errorf(
			"MixedBatch: %s (denied full-tier) must appear in rejected with 'denied'; got rejected=%v",
			deniedFull,
			rejectedRaw,
		)
	}

	// allowedFull must appear in "already_available".
	alreadyRaw, _ := result["already_available"].([]any)
	foundAvail := false
	for _, v := range alreadyRaw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("MixedBatch: unexpected type %T in already_available, want string", v)
		}
		if strings.Contains(s, allowedFull) {
			foundAvail = true
		}
	}
	if !foundAvail {
		t.Errorf("MixedBatch: %s (allowed full-tier) must appear in already_available; got %v", allowedFull, alreadyRaw)
	}
}

// TestExecLoad_MixedBatch_DeniedOnlyFullTierIsError verifies that a batch of
// [denied-full-tier, allowed-lazy] returns an error because the denial must be
// surfaced, even though a lazy tool could load.
//
// Wait — the spec says: "response loads the lazy, lists the denied-full in rejected
// with the policy reason, and acknowledges the allowed-full as already-available.
// None of the three is silently dropped." The overall result is NOT an error when
// a lazy tool loads. The test above covers the success case. This test verifies
// the strictly-denied case (no lazy tools load) remains an error.
func TestExecLoad_AllDenied_IsError(t *testing.T) {
	const deniedFull = "read_file"
	if ToolManifestTier(deniedFull) != ManifestFull {
		t.Skipf("%s must be ManifestFull", deniedFull)
	}

	canLoad := func(_ context.Context, name string) (bool, string) {
		return false, name + " — denied by policy"
	}
	markLoaded := func(_ context.Context, names []string) (map[string]any, []string) {
		return map[string]any{}, nil
	}

	tt := NewToolsTool(nil, 5, 10)
	tt.SetResolver(canLoad, markLoaded)

	r := tt.Execute(context.Background(), map[string]any{
		"names": []any{deniedFull},
	})

	if !r.IsError {
		t.Errorf("AllDenied: expected error when all tools are denied; got: %s", r.ForLLM)
	}
	if !strings.Contains(r.ForLLM, "denied") {
		t.Errorf("AllDenied: error must mention 'denied'; got: %s", r.ForLLM)
	}
}
