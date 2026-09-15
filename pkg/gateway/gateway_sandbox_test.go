// gateway_sandbox_test.go: tests for sandbox and egress - apply the sandbox policy and tool-policy repair at boot

package gateway

import (
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/assert"
)

// --- moved from gateway.go tests 2026-09-15 ---

// TestBuildEgressProxyOrAbort_EmptyAllowList_ConstructionFails_LogsAndContinues
// verifies the UNCHANGED graceful-degradation path: an operator who never
// configured sandbox.egress_allow_list gets the pre-existing behavior — a
// nil proxy and no boot-abort error — even when construction fails.
func TestBuildEgressProxyOrAbort_EmptyAllowList_ConstructionFails_LogsAndContinues(t *testing.T) {
	proxy, err := buildEgressProxyOrAbort(nil, nil, fakeFailingEgressConstructor)
	if !errors.Is(err, errEgressProxyDisabled) {
		t.Fatalf("expected errEgressProxyDisabled (non-fatal) for an empty allow-list, got: %v", err)
	}
	if proxy != nil {
		t.Fatalf("expected nil proxy when construction fails, got: %+v", proxy)
	}

	// Also verify with an explicit empty (non-nil) slice — same contract.
	proxy2, err2 := buildEgressProxyOrAbort([]string{}, nil, fakeFailingEgressConstructor)
	if !errors.Is(err2, errEgressProxyDisabled) {
		t.Fatalf("expected errEgressProxyDisabled (non-fatal) for an explicit empty allow-list, got: %v", err2)
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

// TestResolveAllowGodMode_ConfigGrant_TrueEvenWithFlagOff proves the core
// claim of the UI-driven enablement design: a config-persisted
// sandbox.god_mode_allowed=true grant resolves to allowGodMode=true on the
// NEXT boot, even though the legacy --allow-god-mode CLI flag was not passed.
func TestResolveAllowGodMode_ConfigGrant_TrueEvenWithFlagOff(t *testing.T) {
	cfg := &config.Config{
		Sandbox: config.OmnipusSandboxConfig{GodModeAllowed: true},
	}
	assert.True(t, resolveAllowGodMode(false /* cliFlag */, cfg),
		"sandbox.god_mode_allowed=true must grant availability even when --allow-god-mode was not passed")
}

// TestResolveAllowGodMode_FlagOnly proves the legacy CLI flag alone still
// grants availability, with no config authorization at all — back-compat for
// headless/CI boots.
func TestResolveAllowGodMode_FlagOnly(t *testing.T) {
	cfg := &config.Config{}
	assert.True(t, resolveAllowGodMode(true /* cliFlag */, cfg),
		"--allow-god-mode must grant availability regardless of config")
}

// TestResolveAllowGodMode_NeitherGrant proves the fail-closed default: no
// flag and no config grant resolves to false.
func TestResolveAllowGodMode_NeitherGrant(t *testing.T) {
	cfg := &config.Config{Sandbox: config.OmnipusSandboxConfig{GodModeAllowed: false}}
	assert.False(t, resolveAllowGodMode(false, cfg),
		"neither --allow-god-mode nor sandbox.god_mode_allowed must resolve to unavailable")
}

// TestResolveAllowGodMode_NilConfig proves the defensive nil-cfg path only
// consults the CLI flag and never panics.
func TestResolveAllowGodMode_NilConfig(t *testing.T) {
	assert.False(t, resolveAllowGodMode(false, nil))
	assert.True(t, resolveAllowGodMode(true, nil))
}

// TestResolveAllowGodMode_BothGrants proves the OR is inclusive — both
// sources present still resolves to true (no double-negative surprise).
func TestResolveAllowGodMode_BothGrants(t *testing.T) {
	cfg := &config.Config{Sandbox: config.OmnipusSandboxConfig{GodModeAllowed: true}}
	assert.True(t, resolveAllowGodMode(true, cfg))
}

// TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog is a
// catalog-drift regression test: buildKnownBuiltinToolNames() (this
// package's live-derived tool-policy-coverage universe, walking the general +
// browser + sysagent tool catalogs) must enumerate EXACTLY the same
// tool-name set as pkg/coreagent's hand-maintained allStaticToolNames
// literal (the deny-by-default seed's universe, via denyAllThenOverride),
// read directly via the exported coreagent.AllStaticToolNames() accessor —
// there is no third, hand-copied literal anywhere for this comparison to
// silently go stale against.
//
// pkg/gateway already imports pkg/coreagent directly elsewhere (gateway.go,
// rest.go, rest_tool_registry.go, and several _test.go files), so there is
// no import-cycle risk here: the only reason this comparison previously used
// a hand-copied snapshot literal was that allStaticToolNames itself was an
// unexported package-level var, not any cross-package coupling concern. The
// new AllStaticToolNames() accessor (pkg/coreagent/core.go) resolves that by
// exposing a defensive copy.
//
// The two sides (this package's live registry walk, and coreagent's seed
// literal) are still independently maintained, so nothing else enforces
// they stay in sync — that is exactly what this test exists to catch. A
// failure here means pkg/coreagent's allStaticToolNames and the tool
// registry buildKnownBuiltinToolNames() walks (general + browser + sysagent
// catalogs) have drifted apart — investigate WHICH tool was added, renamed,
// or removed and update BOTH sides to match (never just silence this test):
// coreagent.denyAllThenOverride's deny-by-default seed and this function's
// coverage-validation universe (CLAUDE.md hard constraint 6) must agree on
// the exact same tool-name set, or an agent's seeded policy map ends up with
// either a dead entry or a real tool with no coverage.
func TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog(t *testing.T) {
	known := buildKnownBuiltinToolNames()
	liveNames := make([]string, 0, len(known))
	for name := range known {
		liveNames = append(liveNames, name)
	}
	assert.ElementsMatch(t, coreagent.AllStaticToolNames(), liveNames,
		"buildKnownBuiltinToolNames() (live tool registry, pkg/gateway/gateway.go) and "+
			"pkg/coreagent.AllStaticToolNames() (hand-maintained seed literal, pkg/coreagent/core.go) "+
			"must enumerate the exact same tool-name set — update whichever side is stale "+
			"after confirming which tool actually changed")
}
