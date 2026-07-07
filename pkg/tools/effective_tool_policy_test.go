// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Policy-resolver unification (#438).
//
// These tests pin the single authoritative single-tool resolver
// (EffectiveToolPolicy) and PROVE that FilterToolsByPolicy — the agent-loop tool
// filter — computes the identical per-tool verdict, so the two cannot drift. The
// gateway WS approval hook resolves through the SAME primitive (see
// pkg/gateway/ws_approval_parity_test.go), closing the loop on the two-resolver
// split that previously blocked deny-default agents from executing load_tool.
//
// Scope note: routing FilterToolsByPolicy through EffectiveToolPolicy is a no-op
// for the AGENT-LOOP side (the loop already resolved through the same merge, so
// its verdict is unchanged — that is what FilterParity below proves). It is NOT
// a no-op for the GATEWAY exec gate: it ALIGNS that gate to the loop's
// wildcard-aware verdict. The OLD gateway resolved policy by exact-match only and
// IGNORED wildcard policy keys; the loop's FilterToolsByPolicy always honored
// ".*"/"_*" keys. The wildcard cases in the matrix below pin that (now-shared)
// wildcard semantics with independent hard-coded expectations.

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// effectiveToolPolicyMatrixCase is one (agent, tool) cell of the parity matrix.
type effectiveToolPolicyMatrixCase struct {
	name      string
	cfg       *ToolPolicyCfg
	agentType string
	tool      Tool
	want      string
}

// denyDefaultCfg models an Ava-like custom agent with a small explicit
// allow-list and one explicit ask. There is no default-policy field (CLAUDE.md
// hard constraint 6): a tool with no exact/wildcard entry here fails closed to
// "deny" — which is exactly what the matrix cases below exercise for unlisted
// tools, so the "deny-by-default" name still describes the observed behavior
// even though it is now the fail-closed no-coverage path, not a config field.
func denyDefaultCfg() *ToolPolicyCfg {
	return &ToolPolicyCfg{
		Policies: map[string]string{
			"search_web": "allow", // an explicitly allowed tool
			"fetch_url":  "ask",   // an explicit ask tool
		},
	}
}

// allowDefaultCfg models a custom agent with one explicit deny and one
// explicit ask. There is no default-policy field: an unlisted tool now fails
// closed to "deny" (see the matrix's "now-unlisted tool" case) — the old
// "allow-by-default" config concept this cfg used to model is retired.
func allowDefaultCfg() *ToolPolicyCfg {
	return &ToolPolicyCfg{
		Policies: map[string]string{
			"exec":      "deny", // explicitly denied
			"fetch_url": "ask",  // explicit ask
		},
	}
}

// effectiveToolPolicyMatrix is the shared (agent × tool) matrix exercised by both
// the EffectiveToolPolicy direct test and the FilterToolsByPolicy parity test.
func effectiveToolPolicyMatrix() []effectiveToolPolicyMatrixCase {
	return []effectiveToolPolicyMatrixCase{
		// --- deny-default agent (custom) ---
		{
			name:      "deny-default/load_tool(infra)→allow",
			cfg:       denyDefaultCfg(),
			agentType: "custom",
			tool:      makeScopedTool("load_tool", ScopeGeneral), // real load_tool is ScopeGeneral
			want:      "allow",
		},
		{
			name:      "deny-default/allowed tool→allow",
			cfg:       denyDefaultCfg(),
			agentType: "custom",
			tool:      makeScopedTool("search_web", ScopeGeneral),
			want:      "allow",
		},
		{
			name:      "deny-default/denied (unlisted, default deny)→deny",
			cfg:       denyDefaultCfg(),
			agentType: "custom",
			tool:      makeScopedTool("send_message", ScopeGeneral),
			want:      "deny",
		},
		{
			name:      "deny-default/ask tool→ask",
			cfg:       denyDefaultCfg(),
			agentType: "custom",
			tool:      makeScopedTool("fetch_url", ScopeGeneral),
			want:      "ask",
		},
		{
			name:      "deny-default/ScopeCore tool on custom agent (unlisted)→deny",
			cfg:       denyDefaultCfg(),
			agentType: "custom",
			tool:      makeScopedTool("exec", ScopeCore),
			want:      "deny",
		},
		{
			name: "deny-default/ScopeCore tool on custom agent (explicit allow)→allow",
			cfg: &ToolPolicyCfg{
				Policies: map[string]string{"exec": "allow"},
			},
			agentType: "custom",
			tool:      makeScopedTool("exec", ScopeCore),
			want:      "allow",
		},

		// --- allow-default agent (custom) ---
		{
			name:      "allow-default/load_tool(infra)→allow",
			cfg:       allowDefaultCfg(),
			agentType: "custom",
			tool:      makeScopedTool("load_tool", ScopeGeneral),
			want:      "allow",
		},
		{
			// No default-policy fallback (CLAUDE.md hard constraint 6): a tool with
			// no entry on either side now fails closed to deny — this is the exact
			// "allow-by-default" behavior the constraint retires.
			name:      "allow-default/now-unlisted tool→deny (fail-closed, no default)",
			cfg:       allowDefaultCfg(),
			agentType: "custom",
			tool:      makeScopedTool("search_web", ScopeGeneral),
			want:      "deny",
		},
		{
			name:      "allow-default/explicit deny→deny",
			cfg:       allowDefaultCfg(),
			agentType: "custom",
			tool:      makeScopedTool("exec", ScopeCore),
			want:      "deny",
		},
		{
			name:      "allow-default/explicit ask→ask",
			cfg:       allowDefaultCfg(),
			agentType: "custom",
			tool:      makeScopedTool("fetch_url", ScopeGeneral),
			want:      "ask",
		},

		// --- global deny floor overrides agent allow (strictest-wins) ---
		{
			name: "global-deny-floor overrides agent allow→deny",
			cfg: &ToolPolicyCfg{
				Policies:       map[string]string{"send_message": "allow"},
				GlobalPolicies: map[string]string{"send_message": "deny"},
			},
			agentType: "custom",
			tool:      makeScopedTool("send_message", ScopeGeneral),
			want:      "deny",
		},

		// --- core agent: ScopeCore passes the gate ---
		{
			name:      "core-agent/ScopeCore tool→allow (explicit global allow)",
			cfg:       &ToolPolicyCfg{GlobalPolicies: map[string]string{"exec": "allow"}},
			agentType: "core",
			tool:      makeScopedTool("exec", ScopeCore),
			want:      "allow",
		},

		// --- unknown scope is fail-closed regardless of policy ---
		{
			name:      "unknown-scope tool→deny (fail-closed)",
			cfg:       &ToolPolicyCfg{},
			agentType: "custom",
			tool:      makeScopedTool("weird_tool", ToolScope("bogus")),
			want:      "deny",
		},

		// --- WILDCARD cases (F4): the input class whose verdict the unification
		//     CHANGED for the gateway exec gate (old gateway ignored wildcards).
		//     Hard-coded expectations pin the now-shared wildcard semantics.
		{
			// (a) agent "browser_*":allow on a deny-default agent allows a matching
			//     underscore-wildcard tool.
			name: "wildcard/agent browser_*=allow (deny-default) → browser_navigate allow",
			cfg: &ToolPolicyCfg{
				Policies: map[string]string{"browser_*": "allow"},
			},
			agentType: "custom",
			tool:      makeScopedTool("browser_navigate", ScopeGeneral),
			want:      "allow",
		},
		{
			// (b) global "browser_*":deny floors a matching tool to deny even for an
			//     allow-default agent (strictest-wins on the wildcard floor).
			name: "wildcard/global browser_*=deny (allow-default) → browser_navigate deny",
			cfg: &ToolPolicyCfg{
				GlobalPolicies: map[string]string{"browser_*": "deny"},
			},
			agentType: "custom",
			tool:      makeScopedTool("browser_navigate", ScopeGeneral),
			want:      "deny",
		},
		{
			// (c1) agent "system.*":allow (deny-default) allows a matching dot-wildcard
			//      tool (ScopeCore system tool on a custom agent defers to the merge,
			//      which the wildcard allow satisfies).
			name: "wildcard/agent system.*=allow (deny-default) → matching system tool allow",
			cfg: &ToolPolicyCfg{
				Policies: map[string]string{"system.*": "allow"},
			},
			agentType: "custom",
			tool:      makeScopedTool("system.agent.list", ScopeCore),
			want:      "allow",
		},
		{
			// (c2) the SAME agent policy denies a NON-matching tool (default deny;
			//      "system.*" does not cover "read_file").
			name: "wildcard/agent system.*=allow (deny-default) → non-matching tool deny",
			cfg: &ToolPolicyCfg{
				Policies: map[string]string{"system.*": "allow"},
			},
			agentType: "custom",
			tool:      makeScopedTool("read_file", ScopeGeneral),
			want:      "deny",
		},
	}
}

// TestEffectiveToolPolicy_Matrix verifies the primitive returns the expected
// verdict for every (agent, tool) cell — the security contract: infra always
// allow, deny stays deny (no widening), ask stays ask, global deny floor wins,
// ScopeCore gated on custom agents, unknown scope fail-closed.
func TestEffectiveToolPolicy_Matrix(t *testing.T) {
	for _, tc := range effectiveToolPolicyMatrix() {
		t.Run(tc.name, func(t *testing.T) {
			got := EffectiveToolPolicy(tc.cfg, tc.tool.Scope(), tc.agentType, tc.tool.Name())
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestEffectiveToolPolicy_FilterParity proves NO behavior change on the
// AGENT-LOOP side: FilterToolsByPolicy's per-tool verdict is IDENTICAL to the
// primitive for every cell of the matrix (including the wildcard cells — the loop
// already honored wildcards, so its verdict is unchanged). A tool the primitive
// resolves to "deny" must be ABSENT from FilterToolsByPolicy's output; a tool it
// resolves to "allow"/"ask" must be present with that exact policy in the returned
// policyMap. This guarantees that routing FilterToolsByPolicy through
// EffectiveToolPolicy did not change which tools the model is shown nor their
// effective policy. (The gateway exec gate's wildcard verdict DID change — see the
// file header and the gateway parity test — but that change is toward this same
// loop verdict, not away from it.)
func TestEffectiveToolPolicy_FilterParity(t *testing.T) {
	for _, tc := range effectiveToolPolicyMatrix() {
		t.Run(tc.name, func(t *testing.T) {
			want := EffectiveToolPolicy(tc.cfg, tc.tool.Scope(), tc.agentType, tc.tool.Name())

			filtered, polMap := FilterToolsByPolicy([]Tool{tc.tool}, tc.agentType, tc.cfg)
			name := tc.tool.Name()

			present := false
			for _, ft := range filtered {
				if ft.Name() == name {
					present = true
					break
				}
			}

			if want == "deny" {
				assert.False(t, present,
					"FilterToolsByPolicy must DROP a tool the primitive denies (%s)", name)
				_, inMap := polMap[name]
				assert.False(t, inMap,
					"a denied tool must not appear in the policyMap (%s)", name)
				return
			}

			// allow/ask: must be kept and carry the identical verdict.
			assert.True(t, present,
				"FilterToolsByPolicy must KEEP a tool the primitive allows/asks (%s want %s)", name, want)
			assert.Equal(t, want, polMap[name],
				"FilterToolsByPolicy policyMap verdict must equal the primitive's (%s)", name)
		})
	}
}

// TestEffectiveToolPolicy_GodModeFloorsAllow proves the god-mode override still
// floors every tool's verdict at "allow" through the primitive (so the gateway
// exec gate honors god mode identically to the loop).
func TestEffectiveToolPolicy_GodModeFloorsAllow(t *testing.T) {
	cfg := &ToolPolicyCfg{
		Policies:       map[string]string{"exec": "deny"},
		GlobalPolicies: map[string]string{"exec": "deny"},
		GodMode:        true,
	}
	// Even a doubly-denied ScopeCore tool on a custom agent is allowed under god mode.
	assert.Equal(t, "allow",
		EffectiveToolPolicy(cfg, ScopeCore, "custom", "exec"))
	// And it agrees with the filter.
	filtered, polMap := FilterToolsByPolicy([]Tool{makeScopedTool("exec", ScopeCore)}, "custom", cfg)
	assert.Len(t, filtered, 1)
	assert.Equal(t, "allow", polMap["exec"])
}

// TestEffectiveToolPolicy_NilCfg verifies nil-cfg handling matches
// FilterToolsByPolicy: infra force-allows unconditionally (registration-gated,
// not policy-gated), but a known-scope tool with no policy maps to resolve
// from now fails closed to "deny" — CLAUDE.md hard constraint 6 forbids the
// historical hardcoded "allow" fallback.
func TestEffectiveToolPolicy_NilCfg(t *testing.T) {
	assert.Equal(t, "allow", EffectiveToolPolicy(nil, ScopeGeneral, "custom", "load_tool"))
	assert.Equal(t, "deny", EffectiveToolPolicy(nil, ScopeGeneral, "custom", "search_web"))
	// Unknown scope is still fail-closed even with a nil cfg.
	assert.Equal(t, "deny", EffectiveToolPolicy(nil, ToolScope("bogus"), "custom", "x"))
}
