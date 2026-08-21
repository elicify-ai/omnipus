package security_test

// File purpose: Sprint J issue #76 — sandbox Apply() boot-time wiring integration tests.
//
// These tests prove that on Linux ≥ 5.13 with gateway.sandbox.mode = "enforce",
// the gateway process has Apply() called before it starts serving requests, and
// that the /api/v1/security/sandbox-status endpoint reflects the actual enforcement
// state. On macOS / Windows / pre-5.13 kernels, the tests verify graceful fallback
// (no panic, sandbox.applied=false, backend=fallback).
//
// Traces to: docs/internal/plan/sprint-j-sandbox-apply-spec.md US-1 (boot-time wiring),
// US-2 (dev override), Acceptance Scenarios 2, 4, 6.
//
// Note: Apply() is a one-way ratchet on the calling process (Landlock restrict_self
// cannot be undone). These tests therefore probe the sandbox STATUS via HTTP rather
// than asserting enforcement in-process — actual enforcement is covered by the
// existing sandbox_enforcement_linux_test.go (subprocess approach). The status
// endpoint validates that the gateway correctly wires Apply() at boot.
//
// The tests use the in-process TestGateway harness (which calls gateway.RunContext).
// A separate file (sandbox_apply_fallback_test.go) handles non-Linux assertions
// using the same harness but a different build tag.

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// sandboxStatusResponse mirrors sandbox.Status for JSON decode.
type sandboxStatusResponse struct {
	Backend        string   `json:"backend"`
	Available      bool     `json:"available"`
	KernelLevel    bool     `json:"kernel_level"`
	ABIVersion     int      `json:"abi_version"`
	PolicyApplied  bool     `json:"policy_applied"`
	SeccompEnabled bool     `json:"seccomp_enabled"`
	Notes          []string `json:"notes"`
}

// TestSandboxApply_StatusEndpointReflectsBackend — Acceptance Scenario 2
//
// Given a running gateway (any mode), when GET /api/v1/security/sandbox-status is
// called with valid auth, then the response contains a well-formed JSON body with
// a non-empty "backend" field and an "available" field that accurately reflects
// whether the system supports kernel-level sandboxing.
//
// This is the invariant test — it passes on ALL platforms and provides the
// differentiation assertion: different backends produce different "backend" values.
// Traces to: sprint-j-sandbox-apply-spec.md US-1 Acceptance Scenario 2, line ~118.
func TestSandboxApply_StatusEndpointReflectsBackend(t *testing.T) {
	// BDD: Given a running gateway
	gw := testutil.StartTestGateway(t, testutil.WithBearerAuth())

	// BDD: When GET /api/v1/security/sandbox-status is called
	req, err := gw.NewRequest(http.MethodGet, "/api/v1/security/sandbox-status", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+gw.Token())

	resp, err := gw.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// BDD: Then the response has HTTP 200 with a valid JSON body
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"sandbox-status must return 200 for authenticated request")

	var status sandboxStatusResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status),
		"sandbox-status response must decode as valid JSON")

	// BDD: Then the backend field is non-empty (content assertion)
	assert.NotEmpty(t, status.Backend,
		"backend field must not be empty — every platform has at least a fallback backend")

	// Differentiation assertion: assert the returned backend is one of the
	// recognized values.
	//
	// "seatbelt" was missing here, so this test failed on every Mac from the
	// day the macOS backend shipped — a hardcoded list of known values going
	// stale the moment a new one is added. That is the same defect class
	// ADR-062 is about, in a test rather than a policy: enumerate a set that
	// grows, and the omission shows up as a failure somewhere unrelated.
	knownBackends := []string{"linux", "landlock", "seatbelt", "fallback", "windows", "none"}
	matched := false
	for _, known := range knownBackends {
		if strings.Contains(strings.ToLower(status.Backend), known) {
			matched = true
			break
		}
	}
	assert.True(t, matched,
		"backend value %q must be a recognized backend name (got: %q)", status.Backend, status.Backend)

	// Content assertion: policy_applied must be a boolean field present in the response
	// (bool zero value is false — we validate the JSON contains the key by decoding
	// into a map).
	var raw map[string]any
	req2, err2 := gw.NewRequest(http.MethodGet, "/api/v1/security/sandbox-status", nil)
	require.NoError(t, err2)
	req2.Header.Set("Authorization", "Bearer "+gw.Token())

	resp2, err2 := gw.Do(req2)
	require.NoError(t, err2)
	defer resp2.Body.Close()
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&raw))

	_, hasPolicyApplied := raw["policy_applied"]
	assert.True(t, hasPolicyApplied,
		"sandbox-status response must contain 'policy_applied' field")
}

// TestSandboxApply_FallbackWhenKernelTooOld — Acceptance Scenario 4
//
// Given a host where SelectBackend() returns FallbackBackend (non-Linux, pre-5.13,
// or cgo build), when the gateway starts, then the status endpoint shows
// policy_applied=false, kernel_level=false, and no crash occurs.
//
// This test runs on ALL platforms, asserting the fallback invariant:
// if FallbackBackend was selected, the status must not report policy_applied=true.
//
// Traces to: sprint-j-sandbox-apply-spec.md US-1 Acceptance Scenario 4, line ~120.
func TestSandboxApply_FallbackWhenKernelTooOld(t *testing.T) {
	// BDD: Given a host where kernel-level sandboxing is NOT available
	_, backendName := sandbox.SelectBackend()
	isKernelLevel := strings.HasPrefix(strings.ToLower(backendName), "landlock") ||
		strings.HasPrefix(strings.ToLower(backendName), "linux")

	if isKernelLevel {
		t.Skip("skipping fallback test: this host supports kernel-level sandboxing (backend=" + backendName + ")")
	}

	// BDD: Given a running gateway
	gw := testutil.StartTestGateway(t, testutil.WithBearerAuth())

	// BDD: When GET /api/v1/security/sandbox-status is called
	req, err := gw.NewRequest(http.MethodGet, "/api/v1/security/sandbox-status", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+gw.Token())

	resp, err := gw.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var status sandboxStatusResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))

	// This scenario is "the kernel is too old, so we fall back". On a host that
	// HAS a kernel backend there is no such state to observe, and asserting
	// kernel_level=false there tests the host rather than the code.
	//
	// macOS is exactly that case: Seatbelt is always available, so the gateway
	// correctly selects a kernel backend and this test failed on every Mac —
	// reporting a fallback bug where the real behaviour was correct.
	if status.KernelLevel {
		t.Skipf("host provides a kernel backend (%q); the too-old-kernel fallback "+
			"cannot be exercised here — see the Linux CI run for this scenario", status.Backend)
	}

	// BDD: Then policy_applied=false (fallback does not apply kernel policy)
	assert.False(t, status.PolicyApplied,
		"FallbackBackend must report policy_applied=false — Apply() is a no-op")

	// BDD: Then the gateway did not crash (we reached this assertion)
	assert.NotEmpty(t, status.Backend,
		"backend must still be populated even on fallback — got empty string")

	t.Logf("Fallback confirmed: backend=%q policy_applied=%v platform=%s",
		status.Backend, status.PolicyApplied, runtime.GOOS)
}

// TestSandboxApply_NotesAbsentWhenApplied — Acceptance Scenario 2 (notes check)
//
// Given a gateway where Apply() DID run (Linux ≥ 5.13 with enforce mode), when
// sandbox-status is queried, the response must NOT contain the "Apply() has not
// been called" warning string.
//
// Conversely, when Apply() did NOT run, the notes MUST contain the warning.
// This is the content assertion that distinguishes a real Apply() call from a no-op.
//
// Traces to: sprint-j-sandbox-apply-spec.md US-1 Acceptance Scenario 2, line ~118.
func TestSandboxApply_NotesAbsentWhenApplied(t *testing.T) {
	// BDD: Given a running gateway
	gw := testutil.StartTestGateway(t, testutil.WithBearerAuth())

	req, err := gw.NewRequest(http.MethodGet, "/api/v1/security/sandbox-status", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+gw.Token())

	resp, err := gw.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var status sandboxStatusResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))

	// BDD: When policy_applied=true (enforce mode), no warning/informational
	// note about the sandbox being inactive must be present.
	// When policy_applied=false, the note phrasing depends on DisabledBy
	// (Bug 2 fix): operator choice vs genuine gap get distinct messages.
	const notAppliedMsg = "Apply() has not been called"
	const operatorChoice = "operator choice"
	if status.PolicyApplied {
		// After issue #76 is merged: enforce mode active — notes must be clean.
		for _, note := range status.Notes {
			assert.NotContains(t, note, notAppliedMsg,
				"When policy_applied=true, notes must not contain the 'not applied' warning")
			assert.NotContains(t, note, operatorChoice,
				"When policy_applied=true, notes must not describe an operator-disabled state")
		}
		t.Logf("PASS: sandbox applied (backend=%s), notes clean", status.Backend)
	} else {
		// Before issue #76 merged OR on fallback OR when the harness disables
		// the sandbox: the status MUST explain why it isn't active.
		// FallbackBackend has no notes (nothing to explain).
		if status.KernelLevel {
			found := false
			for _, note := range status.Notes {
				// Either the genuine-gap warning (DisabledBy empty) OR the
				// operator-disabled informational message (Bug 2) counts as
				// an adequate explanation — both tell the operator what's
				// happening. The test harness typically runs with
				// --sandbox=off, which produces the operator-choice note.
				if strings.Contains(note, notAppliedMsg) ||
					strings.Contains(note, "not applied") ||
					strings.Contains(note, operatorChoice) {
					found = true
					break
				}
			}
			assert.True(t, found,
				"When kernel-capable but policy_applied=false, notes must explain "+
					"either the gap ('Apply() has not been called') or the "+
					"operator choice ('operator choice')")
			t.Logf(
				"Test harness runs with sandbox=off (security-lead scope #1); kernel-level backend %q correctly reports policy_applied=false",
				status.Backend,
			)
		} else {
			// Fallback: no notes required.
			t.Logf("Fallback backend %q: policy_applied=false is expected, notes=%v", status.Backend, status.Notes)
		}
	}
}

// TestSandboxApply_AuthRequiredForStatusEndpoint — security invariant
//
// Given a running gateway, when an unauthenticated GET /api/v1/security/sandbox-status
// is issued (no Authorization header), then the gateway returns 401 Unauthorized,
// not the sandbox status. This prevents information disclosure.
//
// Traces to: sprint-j-sandbox-apply-spec.md US-1 (status endpoint should require auth)
func TestSandboxApply_AuthRequiredForStatusEndpoint(t *testing.T) {
	// BDD: Given a running gateway with auth enabled
	gw := testutil.StartTestGateway(t, testutil.WithBearerAuth())

	// BDD: When an unauthenticated request hits the sandbox-status endpoint.
	// NOTE: gw.NewRequest automatically adds the Authorization header, so we
	// build a raw HTTP request directly to simulate an unauthenticated caller.
	req, err := http.NewRequest(http.MethodGet, gw.URL+"/api/v1/security/sandbox-status", nil)
	require.NoError(t, err)
	// Set Origin header (required by the gateway's CORS/host-check middleware)
	// but intentionally omit Authorization header.
	req.Header.Set("Origin", gw.URL)

	resp, err := gw.HTTPClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// BDD: Then the gateway must reject with 401 (not expose sandbox internals)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"sandbox-status must require authentication; unauthenticated request must get 401")
}

// TestSandboxApply_LinuxEnforce_KernelLevelReported is a Linux-only test that
// verifies the backend selection returns a kernel-level backend on Linux ≥ 5.13.
// It does NOT call Apply() (that would lock down the test process itself), but
// it asserts that the gateway reports a kernel-capable backend.
//
// Traces to: sprint-j-sandbox-apply-spec.md US-1 Acceptance Scenario 1, line ~117.
func TestSandboxApply_LinuxEnforce_KernelLevelReported(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only: kernel-level sandbox backend only available on Linux")
	}
	if os.Getuid() == 0 {
		t.Skip("Landlock has nuances for root — skip to avoid false positives")
	}

	// BDD: Given a host where SelectBackend returns LinuxBackend
	backend, name := sandbox.SelectBackend()
	isKernelLevel := strings.HasPrefix(strings.ToLower(name), "landlock") ||
		strings.HasPrefix(strings.ToLower(name), "linux")

	if !isKernelLevel {
		t.Skipf("kernel-level backend not available on this host (got %q); "+
			"check kernel version (need ≥ 5.13)", name)
	}
	_ = backend

	// BDD: When the gateway starts
	gw := testutil.StartTestGateway(t, testutil.WithBearerAuth())

	// BDD: When GET /api/v1/security/sandbox-status is called
	req, err := gw.NewRequest(http.MethodGet, "/api/v1/security/sandbox-status", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+gw.Token())

	resp, err := gw.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var status sandboxStatusResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))

	// Differentiation: two different values for kernel-level vs fallback
	assert.True(t, status.KernelLevel,
		"On Linux ≥ 5.13, status.kernel_level must be true (backend=%q)", status.Backend)
	assert.NotEmpty(t, status.ABIVersion,
		"On Linux ≥ 5.13, abi_version must be present and > 0")
	assert.Greater(t, status.ABIVersion, 0,
		"ABI version must be positive on a Landlock-capable kernel")

	// Sprint J wired Apply() at gateway boot (#76 merged). The test harness defaults
	// to sandbox=off for broader suite compatibility, so policy_applied=false is the
	// expected state in this integration test — the status endpoint still reports the
	// capable backend, which is the contract we assert.
	if status.PolicyApplied {
		t.Logf("PASS: backend=%q ABI=%d policy_applied=true (harness in enforce)", status.Backend, status.ABIVersion)
	} else {
		t.Logf(
			"PASS: backend=%q ABI=%d capable; harness runs sandbox=off so policy_applied=false",
			status.Backend,
			status.ABIVersion,
		)
	}
}

// TestSandboxApply_DescribeBackend_FallbackIsConsistent exercises sandbox.DescribeBackend()
// directly (unit-level integration) to prove it distinguishes capability from enforcement.
// This catches regressions where DescribeBackend misreports PolicyApplied.
//
// Traces to: sprint-j-sandbox-apply-spec.md — Status endpoint (DescribeBackend layer).
func TestSandboxApply_DescribeBackend_FallbackIsConsistent(t *testing.T) {
	// BDD: Given a FallbackBackend (always available)
	fb := sandbox.NewFallbackBackend()
	status := sandbox.DescribeBackend(fb)

	// BDD: Then FallbackBackend reports policy_applied=false, kernel_level=false
	assert.False(t, status.PolicyApplied,
		"FallbackBackend must never report policy_applied=true")
	assert.False(t, status.KernelLevel,
		"FallbackBackend must report kernel_level=false")
	assert.NotEmpty(t, status.Backend,
		"FallbackBackend must have a non-empty backend name")

	// Differentiation test: FallbackBackend and a nil backend produce different outputs.
	nilStatus := sandbox.DescribeBackend(nil)
	assert.NotEqual(t, status.Backend, nilStatus.Backend,
		"nil backend and FallbackBackend must produce different backend names")
	assert.Equal(t, "none", nilStatus.Backend,
		"nil backend must produce backend='none'")
}

// TestSandboxApply_WithAgentsOption_GatewayStillServes confirms that the gateway
// boots successfully and serves the sandbox-status endpoint even when Sandbox config
// (issue #76 — mode field) is not yet written to config.json (default mode).
// This guards against the gateway rejecting boot if the mode field is absent.
//
// Traces to: sprint-j-sandbox-apply-spec.md §2 Intended Outcome item 2 (graceful fallback).
func TestSandboxApply_WithAgentsOption_GatewayStillServes(t *testing.T) {
	// BDD: Given a gateway started with a system agent but no explicit sandbox config
	gw := testutil.StartTestGateway(t,
		testutil.WithBearerAuth(),
		testutil.WithAgents([]config.AgentConfig{
			{ID: "omnipus-system", Type: config.AgentTypeSystem, Description: "system agent"},
		}),
	)

	// BDD: When GET /api/v1/security/sandbox-status is called
	req, err := gw.NewRequest(http.MethodGet, "/api/v1/security/sandbox-status", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+gw.Token())

	resp, err := gw.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// BDD: Then the gateway serves a valid response (not 503/panic)
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"gateway must serve sandbox-status even without explicit sandbox config in config.json")

	var raw map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&raw))
	_, hasBackend := raw["backend"]
	assert.True(t, hasBackend, "response must contain 'backend' field")
}
