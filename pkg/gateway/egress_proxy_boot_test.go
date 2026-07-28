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

// TestBuildEgressProxyOrAbort_EmptyAllowList_ConstructionFails_LogsAndContinues
// verifies the UNCHANGED graceful-degradation path: an operator who never
// configured sandbox.egress_allow_list gets the pre-existing behavior — a
// nil proxy and no boot-abort error — even when construction fails.
func TestBuildEgressProxyOrAbort_EmptyAllowList_ConstructionFails_LogsAndContinues(t *testing.T) {
	proxy, err := buildEgressProxyOrAbort(nil, nil, fakeFailingEgressConstructor)
	if err != nil {
		t.Fatalf("expected no boot-abort error for an empty allow-list, got: %v", err)
	}
	if proxy != nil {
		t.Fatalf("expected nil proxy when construction fails, got: %+v", proxy)
	}

	// Also verify with an explicit empty (non-nil) slice — same contract.
	proxy2, err2 := buildEgressProxyOrAbort([]string{}, nil, fakeFailingEgressConstructor)
	if err2 != nil {
		t.Fatalf("expected no boot-abort error for an explicit empty allow-list, got: %v", err2)
	}
	if proxy2 != nil {
		t.Fatalf("expected nil proxy when construction fails, got: %+v", proxy2)
	}
}

// TestBuildEgressProxyOrAbort_NonEmptyAllowList_ConstructionFails_AbortsBoot
// is the core proof for this fix: an operator who explicitly configured
// sandbox.egress_allow_list gets a *SandboxBootError (not a Warn-and-continue)
// when construction fails, so cmd/omnipus's existing EX_CONFIG (78) exit-code
// mapping (FR-J-004) fires instead of silently booting with unrestricted
// egress for web_serve dev-mode and bash.
func TestBuildEgressProxyOrAbort_NonEmptyAllowList_ConstructionFails_AbortsBoot(t *testing.T) {
	allowList := []string{"registry.npmjs.org", "*.github.com"}
	proxy, err := buildEgressProxyOrAbort(allowList, nil, fakeFailingEgressConstructor)
	if proxy != nil {
		t.Fatalf("expected nil proxy on construction failure, got: %+v", proxy)
	}
	if err == nil {
		t.Fatalf("expected a boot-abort error for a non-empty allow-list; got nil")
	}

	var sbErr *SandboxBootError
	if !errors.As(err, &sbErr) {
		t.Fatalf("expected *SandboxBootError (so cmd/omnipus maps to EX_CONFIG=78); got %T: %v", err, err)
	}

	// The underlying construction error must still be reachable through the
	// wrapper chain for programmatic introspection.
	if !strings.Contains(err.Error(), "simulated egress proxy construction failure") {
		t.Errorf("expected the underlying construction error to be included in the message; got: %s", err.Error())
	}

	// The message must name the configuration field and the entry count so
	// an operator can act on it without reading source.
	msg := err.Error()
	if !strings.Contains(msg, "sandbox.egress_allow_list") {
		t.Errorf("expected error message to name sandbox.egress_allow_list; got: %s", msg)
	}
	if !strings.Contains(msg, "2 entr") {
		t.Errorf("expected error message to mention the allow-list entry count (2); got: %s", msg)
	}
}

// TestBuildEgressProxyOrAbort_RealConstructor_MalformedAllowListEntry_AbortsBoot
// exercises the REAL sandbox.NewEgressProxy (not the fake) to prove the fix
// works end-to-end against production code, not just the injected test
// double. A "**" entry deterministically fails compileEgressAllowList before
// any port is bound (pkg/sandbox/egress_proxy.go's compileEgressAllowList),
// so this is a reliable, non-flaky way to force sandbox.NewEgressProxy to
// return a real error without relying on a forced port conflict.
func TestBuildEgressProxyOrAbort_RealConstructor_MalformedAllowListEntry_AbortsBoot(t *testing.T) {
	allowList := []string{"exa**mple.com"}
	proxy, err := buildEgressProxyOrAbort(allowList, nil, sandbox.NewEgressProxy)
	if proxy != nil {
		t.Fatalf("expected nil proxy on real construction failure, got: %+v", proxy)
	}
	if err == nil {
		t.Fatalf("expected a boot-abort error for a malformed non-empty allow-list; got nil")
	}
	var sbErr *SandboxBootError
	if !errors.As(err, &sbErr) {
		t.Fatalf("expected *SandboxBootError; got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "unsupported '**' wildcard") {
		t.Errorf("expected the real compileEgressAllowList error text to surface; got: %s", err.Error())
	}
}

// TestBuildEgressProxyOrAbort_Success_ReturnsProxy is a sanity check that the
// happy path (construction succeeds) is unaffected: the real proxy is
// returned, no error, and no SandboxBootError is fabricated.
func TestBuildEgressProxyOrAbort_Success_ReturnsProxy(t *testing.T) {
	proxy, err := buildEgressProxyOrAbort([]string{"example.com"}, nil, sandbox.NewEgressProxy)
	if err != nil {
		t.Fatalf("expected no error on successful construction, got: %v", err)
	}
	if proxy == nil {
		t.Fatalf("expected a non-nil proxy on success")
	}
	t.Cleanup(func() {
		if closeErr := proxy.Close(); closeErr != nil {
			t.Logf("proxy.Close: %v", closeErr)
		}
	})
	if proxy.Addr() == "" {
		t.Errorf("expected the constructed proxy to have a bound address")
	}
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
