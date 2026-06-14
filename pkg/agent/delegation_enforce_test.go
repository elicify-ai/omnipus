package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// intPtr is a local helper for building *int policy fields.
func intPtr(n int) *int { return &n }

// ctxAtDepth returns a context carrying a turnState at the given delegation depth.
func ctxAtDepth(depth int) context.Context {
	return withTurnState(context.Background(), &turnState{depth: depth})
}

// agentWithPolicy builds an AgentConfig carrying the given delegation policy.
func agentWithPolicy(id string, p *config.DelegationPolicy) *config.AgentConfig {
	return &config.AgentConfig{ID: id, DelegationPolicy: p}
}

// --- FR-6.2: targeted delegation gate (spawn = "background", task = "task") ---

func TestDelegationDenyChecker_AllowedWhenModeTrustDepthPermit(t *testing.T) {
	// Policy: may delegate to "ray" in background mode, depth cap 3.
	cfg := agentWithPolicy("mia", &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ray"}},
		Modes: []config.DelegationMode{config.DelegationModeBackground},
		Depth: intPtr(3),
	})
	check := buildDelegationDenyChecker("mia", cfg, config.AgentDefaults{},
		config.DelegationModeBackground, nil)

	// current depth 0 < cap 3, target trusted, mode permitted → allowed (no reason).
	if reason := check(ctxAtDepth(0), "ray"); reason != "" {
		t.Fatalf("expected delegation allowed, got deny reason: %q", reason)
	}
}

func TestDelegationDenyChecker_DeniedWhenTargetNotTrusted(t *testing.T) {
	cfg := agentWithPolicy("mia", &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ray"}},
		Modes: []config.DelegationMode{config.DelegationModeBackground},
	})
	check := buildDelegationDenyChecker("mia", cfg, config.AgentDefaults{},
		config.DelegationModeBackground, nil)

	reason := check(ctxAtDepth(0), "ava") // ava not in To
	if reason == "" {
		t.Fatal("expected delegation denied for untrusted target, got allow")
	}
	if !strings.Contains(reason, "trust set") {
		t.Fatalf("expected trust-set reason, got: %q", reason)
	}
}

func TestDelegationDenyChecker_DeniedWhenModeForbidden(t *testing.T) {
	// Policy permits only the "task" mode; a background spawn must be rejected.
	cfg := agentWithPolicy("mia", &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ray"}},
		Modes: []config.DelegationMode{config.DelegationModeTask},
	})
	check := buildDelegationDenyChecker("mia", cfg, config.AgentDefaults{},
		config.DelegationModeBackground, nil)

	reason := check(ctxAtDepth(0), "ray") // target trusted, but mode not allowed
	if reason == "" {
		t.Fatal("expected delegation denied for forbidden mode, got allow")
	}
	if !strings.Contains(reason, "mode") {
		t.Fatalf("expected mode reason, got: %q", reason)
	}
}

func TestDelegationDenyChecker_DeniedWhenDepthExceeded(t *testing.T) {
	// Depth cap 2; current chain depth 2 → at the cap → deny further delegation.
	cfg := agentWithPolicy("mia", &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ray"}},
		Modes: []config.DelegationMode{config.DelegationModeBackground},
		Depth: intPtr(2),
	})
	check := buildDelegationDenyChecker("mia", cfg, config.AgentDefaults{},
		config.DelegationModeBackground, nil)

	if reason := check(ctxAtDepth(1), "ray"); reason != "" {
		t.Fatalf("depth 1 < cap 2 should be allowed, got deny: %q", reason)
	}
	reason := check(ctxAtDepth(2), "ray") // depth 2 >= cap 2 → deny
	if reason == "" {
		t.Fatal("expected delegation denied at depth cap, got allow")
	}
	if !strings.Contains(reason, "depth") {
		t.Fatalf("expected depth reason, got: %q", reason)
	}
}

func TestDelegationDenyChecker_UntargetedSkipsTrustButEnforcesMode(t *testing.T) {
	// No explicit target (agent_id == ""): trust check is skipped, but mode
	// is still enforced.
	cfg := agentWithPolicy("mia", &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ray"}},
		Modes: []config.DelegationMode{config.DelegationModeTask}, // background forbidden
	})
	check := buildDelegationDenyChecker("mia", cfg, config.AgentDefaults{},
		config.DelegationModeBackground, nil)

	reason := check(ctxAtDepth(0), "")
	if reason == "" || !strings.Contains(reason, "mode") {
		t.Fatalf("expected mode denial for untargeted background spawn, got: %q", reason)
	}
}

// --- FR-6.2: synchronous subagent gate (mode = "await") ---

func TestSubagentDelegationDenyChecker_AllowedWhenPermitted(t *testing.T) {
	cfg := agentWithPolicy("mia", &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ray"}},
		Modes: []config.DelegationMode{config.DelegationModeAwait},
		Depth: intPtr(3),
	})
	check := buildSubagentDelegationDenyChecker(cfg, config.AgentDefaults{})

	if reason := check(ctxAtDepth(0)); reason != "" {
		t.Fatalf("expected sync delegation allowed, got deny: %q", reason)
	}
}

func TestSubagentDelegationDenyChecker_DeniedWhenNoTargets(t *testing.T) {
	// Explicit empty To means "deny all delegation".
	cfg := agentWithPolicy("mia", &config.DelegationPolicy{
		To: []config.AgentRef{},
	})
	check := buildSubagentDelegationDenyChecker(cfg, config.AgentDefaults{})

	reason := check(ctxAtDepth(0))
	if reason == "" {
		t.Fatal("expected sync delegation denied with empty To, got allow")
	}
}

func TestSubagentDelegationDenyChecker_DeniedWhenModeForbidden(t *testing.T) {
	// Policy allows delegation targets but not the "await" mode.
	cfg := agentWithPolicy("mia", &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ray"}},
		Modes: []config.DelegationMode{config.DelegationModeBackground}, // await forbidden
	})
	check := buildSubagentDelegationDenyChecker(cfg, config.AgentDefaults{})

	reason := check(ctxAtDepth(0))
	if reason == "" || !strings.Contains(reason, "mode") {
		t.Fatalf("expected mode denial for await, got: %q", reason)
	}
}

func TestSubagentDelegationDenyChecker_DeniedWhenDepthExceeded(t *testing.T) {
	cfg := agentWithPolicy("mia", &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ray"}},
		Modes: []config.DelegationMode{config.DelegationModeAwait},
		Depth: intPtr(1),
	})
	check := buildSubagentDelegationDenyChecker(cfg, config.AgentDefaults{})

	reason := check(ctxAtDepth(1)) // depth 1 >= cap 1
	if reason == "" || !strings.Contains(reason, "depth") {
		t.Fatalf("expected depth denial, got: %q", reason)
	}
}

func TestCurrentDelegationDepth_DefaultsToZeroWithoutTurnState(t *testing.T) {
	if d := currentDelegationDepth(context.Background()); d != 0 {
		t.Fatalf("expected depth 0 without turnState, got %d", d)
	}
	if d := currentDelegationDepth(ctxAtDepth(4)); d != 4 {
		t.Fatalf("expected depth 4 from turnState, got %d", d)
	}
}
