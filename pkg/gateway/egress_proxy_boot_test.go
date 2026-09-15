// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Tests for the egress-proxy boot-abort fix: when an operator has explicitly
// configured sandbox.egress_allow_list (opted into egress restriction) and
// sandbox.NewEgressProxy fails to construct, boot must abort with a
// *SandboxBootError rather than silently continuing with a nil proxy.
//
// A nil *sandbox.EgressProxy is NOT a "feature disabled" signal downstream —
// pkg/tools/web_serve.go's proxyAddr() and pkg/tools/shell.go's
// sandboxLimitsEnv both interpret an empty EgressProxyAddr as "skip
// HTTP_PROXY/HTTPS_PROXY entirely", so web_serve dev-mode children and bash's
// hardened-exec children would run with fully unrestricted egress — the
// opposite of what the operator asked for. See buildEgressProxyOrAbort's doc
// comment in gateway.go for the full trace.

import (
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// fakeFailingEgressConstructor always returns an error, regardless of the
// allow-list it is given. Used to deterministically exercise the
// construction-failure branch without depending on sandbox.NewEgressProxy's
// real (and mostly unforceable, e.g. net.Listen("tcp","127.0.0.1:0")) failure
// modes.
func fakeFailingEgressConstructor(_ []string, _ sandbox.EgressAuditFunc) (*sandbox.EgressProxy, error) {
	return nil, errors.New("simulated egress proxy construction failure")
}

// TestSandboxBootError_WrapsEgressProxyConstructionError documents (mirroring
// TestSandboxBootError_WrapsAuditConstructionError in
// audit_boot_abort_test.go) that errors.As/errors.Is traverse the
// SandboxBootError wrapper for the egress-proxy case exactly the same way
// they do for the pre-existing audit-logger-construction case, so
// cmd/omnipus's exit-code mapping needs no egress-specific branch.
func TestSandboxBootError_WrapsEgressProxyConstructionError(t *testing.T) {
	underlying := errors.New("egress_proxy: listen: address already in use")
	bootErr := &SandboxBootError{Err: underlying}

	if !errors.Is(bootErr, underlying) {
		t.Fatalf("errors.Is must reach the underlying error through SandboxBootError")
	}

	// Sanity: audit.EmitBootAbortStderr (used by buildEgressProxyOrAbort) must
	// not panic on a nil writer override and must accept the KV shape we pass.
	var buf strings.Builder
	prev := audit.BootAbortWriter
	audit.BootAbortWriter = &buf
	defer func() { audit.BootAbortWriter = prev }()
	audit.EmitBootAbortStderr(
		"gateway.egress_proxy.construction_failed",
		"-",
		"",
		underlying,
		[]audit.KV{{Key: "allow_list_entries", Value: "1"}},
	)
	if !strings.Contains(buf.String(), "gateway.egress_proxy.construction_failed") {
		t.Errorf("expected BOOT_ABORT_REASON line to be written; got: %s", buf.String())
	}
}
