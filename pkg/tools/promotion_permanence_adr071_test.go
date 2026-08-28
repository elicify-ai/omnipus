// Omnipus — ADR-071 §1.1.1 / D3 FR-037 / FR-037a
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// promotion_permanence_adr071_test.go pins the split between the two
// structurally independent promotion mechanisms ADR-071 §1.1.1 documents as
// its own most load-bearing correction (CRIT-101):
//
//   - Static catalog tools (IsCore: true): PromoteTools/TickTTL are literal
//     no-ops on them, and GetAll admits a core entry UNCONDITIONALLY. There is
//     no decay path — FR-037 forbids building one against this mechanism.
//   - Externally-provided (MCP) tools (IsCore: false): PromoteTools stamps a
//     TTL, TickTTL decrements it once per turn, and GetAll requires TTL > 0.
//     This is PRE-EXISTING, untouched behavior — FR-037a requires it keep
//     decaying exactly as it does today.
//
// Both tests live in this ONE file, per the spec's explicit requirement, so
// the split is documented where an implementer will meet it — the two
// assertions are opposite outcomes on purpose, not a contradiction.

package tools

import "testing"

// TestStaticPromotion_SurvivesBeyondDiscoveryLifetime is the FR-037 half: a
// static (IsCore) tool admitted via PromoteTools remains in GetAll()'s output
// no matter how many times TickTTL runs, because TickTTL's decrement is
// guarded by `!entry.IsCore` and GetAll's admission test is
// `entry.IsCore || entry.TTL > 0` — the TTL path is a structural no-op for
// every static tool. This is the plain multi-turn case AND the
// past-the-boundary case in one test: driving more ticks than any configured
// MCP discovery TTL would use is a strict superset of the plain case.
func TestStaticPromotion_SurvivesBeyondDiscoveryLifetime(t *testing.T) {
	r := NewToolRegistry()
	const name = "static_lazy_tool"
	r.Register(newMockTool(name, "a static, core-registered lazy tool"))

	// Promote it exactly as the ToolSearch query path does.
	const ttl = 3
	r.PromoteTools([]string{name}, ttl)

	if _, ok := r.Get(name); !ok {
		t.Fatalf("fixture: %q must be gettable immediately after PromoteTools", name)
	}

	// Tick WAY past the configured discovery lifetime (default 5, and past
	// this test's own ttl=3) — a static tool must never decay.
	for i := 0; i < 50; i++ {
		r.TickTTL()
	}

	if _, ok := r.Get(name); !ok {
		t.Errorf("FR-037: static tool %q must remain gettable after 50 TickTTL calls — "+
			"no decay path exists for the static usable-set mechanism", name)
	}

	found := false
	for _, tl := range r.GetAll() {
		if tl.Name() == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("FR-037: static tool %q must remain in GetAll()'s output after 50 TickTTL calls", name)
	}
}

// TestExternalPromotion_DecaysWithDiscoveryLifetime is the FR-037a half — the
// mirror assertion, deliberately opposite: an externally-provided (MCP,
// IsCore:false) tool DOES stop being usable once its TTL is ticked to zero.
// This is NOT the eviction FR-037 forbids — it exercises PromoteTools/TickTTL
// on a non-core entry, a structurally different mechanism than the
// loadedTools record FR-037 governs, and this behavior is pre-existing and
// explicitly required to remain unchanged by ADR-071.
func TestExternalPromotion_DecaysWithDiscoveryLifetime(t *testing.T) {
	r := NewToolRegistry()
	const name = "external_mcp_tool"
	r.RegisterHidden(newMockTool(name, "an externally-provided MCP tool"))

	// Before promotion: TTL is 0, so the hidden tool is not yet gettable.
	if _, ok := r.Get(name); ok {
		t.Fatalf("fixture: hidden tool %q must NOT be gettable before PromoteTools", name)
	}

	const ttl = 3
	r.PromoteTools([]string{name}, ttl)

	if _, ok := r.Get(name); !ok {
		t.Fatalf("fixture: %q must be gettable immediately after PromoteTools", name)
	}

	// Tick exactly `ttl` times — the tool must decay to unreachable.
	for i := 0; i < ttl; i++ {
		r.TickTTL()
	}

	if _, ok := r.Get(name); ok {
		t.Errorf("FR-037a: external tool %q must no longer be gettable after its TTL (%d) has "+
			"been ticked down to zero — this decay path is pre-existing and must remain unchanged", name, ttl)
	}

	for _, tl := range r.GetAll() {
		if tl.Name() == name {
			t.Errorf("FR-037a: external tool %q must not appear in GetAll()'s output once its TTL reaches zero", name)
		}
	}
}
