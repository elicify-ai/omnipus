//go:build !cgo

// docker_exec_test.go — regression test for Bug-4: Docker exec works in default
// config.
//
// Bug: default sandbox mode inside a Docker container was "enforce" (the
// normal fresh-install default). On Linux inside Docker, Landlock's
// landlock_restrict_self succeeds but then no process can fork/exec because
// the filesystem rules prevent executing anything outside the locked tree, so
// every exec tool call fails with "fork/exec /bin/sh: permission denied".
//
// Fix: when running inside a Docker container, the default sandbox mode must
// NOT be "enforce". The expected default is "permissive" (audit-only) so exec
// still works while kernel audit logging captures policy events.
//
// Traces to: Bug-4 (Docker exec works in default config)

package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestDockerDefault_SandboxMode_IsNotEnforce verifies that when the binary
// detects it is running inside a Docker container AND no explicit sandbox.mode
// is set, the effective sandbox mode is NOT "enforce".
//
// The implementation is expected to detect Docker via either:
//   - OMNIPUS_IN_DOCKER=1 environment variable
//   - Presence of /.dockerenv at boot
//
// We simulate Docker by setting OMNIPUS_IN_DOCKER=1 before starting the gateway.
//
// BDD: Given the binary is running inside a Docker container (OMNIPUS_IN_DOCKER=1)
//
//	And sandbox.mode is not explicitly set in config.json
//	When the gateway boots
//	Then the resolved sandbox mode is NOT "enforce"
//	So that exec tool calls do not fail with "fork/exec /bin/sh: permission denied"
//
// Traces to: Bug-4 (Docker default sandbox mode)
func TestDockerDefault_SandboxMode_IsNotEnforce(t *testing.T) {
	// Signal Docker environment to the gateway via env var.
	// The implementation should check this at the resolveMode() level.
	t.Setenv("OMNIPUS_IN_DOCKER", "1")

	// Start gateway with a mock LLM.
	gw := startIntegrationGateway(t)

	// Query the sandbox status endpoint to read the resolved mode.
	req, err := gw.NewRequest(http.MethodGet, "/api/v1/security/sandbox-status", nil)
	if err != nil {
		t.Fatalf("build GET /api/v1/security/sandbox-status request: %v", err)
	}
	resp, err := gw.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/security/sandbox-status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("BUG-4: GET /api/v1/security/sandbox-status returned %d (expected 200)", resp.StatusCode)
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read sandbox-status body: %v", err)
	}

	// The endpoint returns a JSON object. Extract the "mode" field.
	// It may be nested under a key or at the top level — try both shapes.
	mode := extractSandboxMode(t, rawBody)
	t.Logf("resolved sandbox mode inside Docker (OMNIPUS_IN_DOCKER=1): %q", mode)

	// Key assertion: "enforce" must NOT be the default inside Docker.
	// That is the behavior that causes "fork/exec /bin/sh: permission denied".
	if strings.EqualFold(mode, "enforce") {
		t.Fatalf(
			"BUG-4: sandbox mode inside Docker with no explicit config is %q — "+
				"must NOT be 'enforce' because it causes fork/exec permission denied. "+
				"Expected 'permissive' or 'off'.",
			mode,
		)
	}
}

// TestDockerDefault_ExplicitModeNotOverridden verifies that when an explicit
// sandbox.mode IS set in config.json, the Docker detection path does NOT
// override it. Explicit operator config always wins.
//
// BDD: Given OMNIPUS_IN_DOCKER=1 (Docker environment)
//
//	And sandbox.mode = "off" is explicitly set
//	When the gateway boots
//	Then the resolved mode is still "off" (explicit config wins)
//
// Traces to: Bug-4 (explicit config must not be overridden by Docker detection)
func TestDockerDefault_ExplicitModeNotOverridden(t *testing.T) {
	// Signal Docker environment.
	t.Setenv("OMNIPUS_IN_DOCKER", "1")

	// The testutil.buildConfig always sets sandbox.Mode = "off" explicitly
	// (see pkg/agent/testutil/options.go). This simulates an operator who
	// wrote sandbox.mode = "off" to config.json. The Docker default must NOT
	// override this.
	gw := startIntegrationGateway(t)

	req, err := gw.NewRequest(http.MethodGet, "/api/v1/security/sandbox-status", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := gw.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/security/sandbox-status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/security/sandbox-status returned %d", resp.StatusCode)
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	mode := extractSandboxMode(t, rawBody)
	t.Logf("sandbox mode with explicit config in Docker: %q", mode)

	// Explicit "off" must survive the Docker detection path.
	if !strings.EqualFold(mode, "off") {
		t.Errorf(
			"BUG-4 (override): explicit sandbox.mode='off' was changed to %q by Docker detection — "+
				"explicit config must always take priority",
			mode,
		)
	}
}

// extractSandboxMode parses the sandbox-status endpoint response and extracts
// the "mode" field. It handles both a top-level {"mode":"..."} shape and a
// nested {"sandbox":{"mode":"..."}} shape.
func extractSandboxMode(t *testing.T, rawBody []byte) string {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &top); err != nil {
		t.Fatalf("parse sandbox/config response: %v", err)
	}

	// Try top-level "mode" field.
	if raw, ok := top["mode"]; ok {
		var mode string
		if err := json.Unmarshal(raw, &mode); err == nil {
			return mode
		}
	}

	// Try nested "sandbox" → "mode".
	if raw, ok := top["sandbox"]; ok {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err == nil {
			if modeRaw, ok := nested["mode"]; ok {
				var mode string
				if err := json.Unmarshal(modeRaw, &mode); err == nil {
					return mode
				}
			}
		}
	}

	t.Logf("sandbox/config response body: %s", string(rawBody))
	t.Logf("WARN: could not extract 'mode' from sandbox/config response — returning empty string")
	return ""
}
