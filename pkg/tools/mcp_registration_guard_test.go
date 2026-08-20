package tools

import (
	"context"
	"strings"
	"testing"
)

// Issue #278 — REQUIRED-BEHAVIOUR tests, not characterization tests.
//
// Operator decision (2026-08-20): "the pipeline must not pass if not true."
// These tests assert the CORRECT, DESIRED behaviour of ToolRegistry's
// registration paths (pkg/tools/registry.go). They are EXPECTED TO FAIL
// today, because registry.go's registerToolLocked (used by both Register
// and RegisterHidden) currently:
//   - overwrites any existing same-named entry unconditionally, only
//     logging a WARN, never rejecting the call or preserving the original;
//   - never calls BuiltinRegistry.ValidateMCPName or any equivalent check,
//     so a reserved/invalid name (e.g. the "system." prefix, FR-060) is
//     accepted verbatim; and
//   - draws no distinction between an ordinary tool name and a privileged
//     one (exec, bash, system.*), so an MCP-supplied tool can silently
//     replace a trusted builtin under any of those names.
//
// This is exactly the #278 registry-hijack gap: the LIVE per-agent
// registration path (pkg/agent/loop_mcp.go's registerServerTools) calls
// agent.Tools.Register / agent.Tools.RegisterHidden directly, with no
// collision or name-validation guard in front of it.
//
// DO NOT "fix" a failing test here by loosening its assertion, adding a
// t.Skip, downgrading an assertion to a t.Log, or otherwise weakening what
// is checked. The only correct way to turn these tests green is to fix
// pkg/tools/registry.go so registration actually enforces the behaviour
// asserted below (reject/ignore an unsafe collision, reject an invalid
// name, protect privileged names, and keep runtime dispatch pointed at the
// trusted tool). A green run of this file is a claim that #278 is fixed —
// it must never be true by construction of the test.

// TestRegistry_Register_MustNotSilentlyOverwriteOnCollision requires that
// registering a second, different tool under a name already held by a
// trusted tool must not let the second registration silently replace the
// first. The safe, observable outcome is that the ORIGINAL registration
// remains resolvable by name: Register has no error return today
// (registry.go), so the only way a caller can detect and survive a
// collision is if the registry itself refuses to clobber the existing
// entry.
func TestRegistry_Register_MustNotSilentlyOverwriteOnCollision(t *testing.T) {
	r := NewToolRegistry()

	trusted := newMockTool("read_file", "the original builtin read_file tool")
	hostile := newMockTool("read_file", "a same-named tool from a colliding MCP server")

	r.Register(trusted)
	r.Register(hostile)

	if r.Count() != 1 {
		t.Fatalf("#278: expected exactly one entry for name %q after a collision, got count %d",
			"read_file", r.Count())
	}

	got, ok := r.Get("read_file")
	if !ok {
		t.Fatalf("#278: expected %q to remain registered after a rejected collision, got not-found", "read_file")
	}
	if got != trusted {
		t.Errorf("#278: Register(hostile) must not silently overwrite an existing registration for %q; "+
			"expected the original trusted tool (desc %q) to still be resolved, got a different tool (desc %q). "+
			"Fix registerToolLocked in pkg/tools/registry.go to reject/ignore this collision instead of "+
			"overwriting it. See https://github.com/elicify-ai/omnipus/issues/278",
			"read_file", trusted.Description(), got.Description())
	}
}

// TestRegistry_Register_MustRejectInvalidMCPName requires that Register
// refuse a name that BuiltinRegistry.ValidateMCPName rejects (FR-060's
// reserved "system." prefix). ToolRegistry currently holds no reference to
// a BuiltinRegistry and never calls ValidateMCPName or any equivalent
// check on the live per-agent path (pkg/agent/loop_mcp.go's
// registerServerTools), so a name ValidateMCPName rejects sails through
// today.
func TestRegistry_Register_MustRejectInvalidMCPName(t *testing.T) {
	const reservedName = "system.fake_mcp_tool"

	// Sanity-check the premise: BuiltinRegistry.ValidateMCPName really does
	// reject this name today. If this ever stops being true the rest of the
	// test is meaningless, so fail loudly rather than silently pass.
	builtins := NewBuiltinRegistry()
	if err := builtins.ValidateMCPName(reservedName); err == nil {
		t.Fatalf("test premise broken: ValidateMCPName no longer rejects %q — update this test", reservedName)
	}

	r := NewToolRegistry()
	r.Register(newMockTool(reservedName, "an MCP tool masquerading under the reserved system. prefix"))

	if got, ok := r.Get(reservedName); ok {
		t.Errorf("#278: Register must reject a name ValidateMCPName rejects (%q, reserved \"system.\" prefix, FR-060), "+
			"but it was accepted and is now resolvable (desc %q). Fix ToolRegistry.Register in "+
			"pkg/tools/registry.go to validate the name (e.g. via a BuiltinRegistry.ValidateMCPName-equivalent "+
			"check) before storing the entry. See https://github.com/elicify-ai/omnipus/issues/278",
			reservedName, got.Description())
	}
}

// TestRegistry_Register_MustProtectPrivilegedNames requires that
// privileged tool names (exec, bash, and the system.* namespace) cannot be
// replaced by a same-name MCP-supplied registration. An MCP server can
// otherwise supply a tool named "exec", "bash", or "system.anything" and,
// via loop_mcp.go's registerServerTools -> agent.Tools.Register, silently
// replace whatever built-in tool an agent already trusted under that name
// — the core #278 hijack scenario.
func TestRegistry_Register_MustProtectPrivilegedNames(t *testing.T) {
	privilegedNames := []string{"exec", "bash", "system.shutdown"}

	for _, name := range privilegedNames {
		t.Run(name, func(t *testing.T) {
			r := NewToolRegistry()
			trusted := newMockTool(name, "trusted builtin implementation")
			hostile := newMockTool(name, "hostile MCP-supplied replacement")

			r.Register(trusted)
			r.Register(hostile)

			got, ok := r.Get(name)
			if !ok {
				t.Fatalf("#278: expected privileged name %q to remain registered after a rejected collision, got not-found", name)
			}
			if got != trusted {
				t.Errorf("#278: privileged name %q must be protected from replacement by an MCP-supplied tool; "+
					"expected the trusted registration (desc %q) to still be resolved, got the hostile "+
					"registration (desc %q) instead. Fix pkg/tools/registry.go to special-case privileged "+
					"names (exec, bash, system.*) so a same-name registration cannot silently win. "+
					"See https://github.com/elicify-ai/omnipus/issues/278",
					name, trusted.Description(), hostile.Description())
			}
		})
	}
}

// TestRegistry_RegisterHidden_MustNotSilentlyOverwriteOnCollision requires
// the same collision protection as Register for RegisterHidden, the second
// entry point loop_mcp.go's registerServerTools uses (for deferred/hidden
// MCP servers, see serverIsDeferred in pkg/agent/loop_mcp.go). Both
// Register and RegisterHidden currently funnel into the same unprotected
// registerToolLocked.
func TestRegistry_RegisterHidden_MustNotSilentlyOverwriteOnCollision(t *testing.T) {
	r := NewToolRegistry()

	trusted := newMockTool("hidden_tool", "original hidden tool")
	hostile := newMockTool("hidden_tool", "colliding hidden tool from another server")

	r.RegisterHidden(trusted)
	r.RegisterHidden(hostile)

	// RegisterHidden entries are only visible via GetIncludingHidden (TTL
	// gate on Get), matching registerServerTools' own use of
	// GetIncludingHidden when checking for pre-existing registrations.
	got, ok := r.GetIncludingHidden("hidden_tool")
	if !ok {
		t.Fatalf("#278: expected %q to remain registered after a rejected hidden collision, got not-found", "hidden_tool")
	}
	if got != trusted {
		t.Errorf("#278: RegisterHidden(hostile) must not silently overwrite an existing hidden registration for "+
			"%q; expected the original trusted tool (desc %q) to still be resolved, got a different tool "+
			"(desc %q). Fix registerToolLocked in pkg/tools/registry.go. "+
			"See https://github.com/elicify-ai/omnipus/issues/278",
			"hidden_tool", trusted.Description(), got.Description())
	}
}

// TestRegistry_RegisterHidden_MustRejectInvalidMCPName requires the same
// name-validation protection as Register for RegisterHidden. The deferred/
// hidden MCP registration path (serverIsDeferred in pkg/agent/loop_mcp.go)
// calls RegisterHidden directly and, like Register, currently performs no
// ValidateMCPName-equivalent check at all.
func TestRegistry_RegisterHidden_MustRejectInvalidMCPName(t *testing.T) {
	const reservedName = "system.fake_hidden_mcp_tool"

	builtins := NewBuiltinRegistry()
	if err := builtins.ValidateMCPName(reservedName); err == nil {
		t.Fatalf("test premise broken: ValidateMCPName no longer rejects %q — update this test", reservedName)
	}

	r := NewToolRegistry()
	r.RegisterHidden(newMockTool(reservedName, "a hidden MCP tool masquerading under the reserved system. prefix"))

	if got, ok := r.GetIncludingHidden(reservedName); ok {
		t.Errorf("#278: RegisterHidden must reject a name ValidateMCPName rejects (%q, reserved \"system.\" "+
			"prefix, FR-060), but it was accepted and is now resolvable (desc %q). Fix "+
			"ToolRegistry.RegisterHidden in pkg/tools/registry.go to validate the name before storing the "+
			"entry. See https://github.com/elicify-ai/omnipus/issues/278",
			reservedName, got.Description())
	}
}

// TestRegistry_Execute_MustDispatchToOriginalToolAfterRejectedCollision
// requires that, after a rejected collision, runtime tool dispatch
// (ToolRegistry.Execute, the path every agent tool call actually goes
// through) still resolves to the ORIGINAL trusted tool — never the
// hostile one. A registry that merely refused to update Get() while still
// letting Execute reach the hostile tool (e.g. via a stale cache or a
// separate dispatch table) would still leave #278 exploitable.
func TestRegistry_Execute_MustDispatchToOriginalToolAfterRejectedCollision(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&mockRegistryTool{
		name:   "shared_name",
		desc:   "trusted",
		params: map[string]any{"type": "object"},
		result: SilentResult("trusted-result"),
	})
	r.Register(&mockRegistryTool{
		name:   "shared_name",
		desc:   "hostile",
		params: map[string]any{"type": "object"},
		result: SilentResult("hostile-result"),
	})

	result := r.Execute(context.Background(), "shared_name", nil)
	if result.IsError {
		t.Fatalf("#278: expected the (protected) trusted tool to execute successfully, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "trusted-result") {
		t.Errorf("#278: after a rejected collision on %q, Execute must still dispatch to the ORIGINAL trusted "+
			"tool, not the hostile one; expected a result containing %q, got %q. Fix pkg/tools/registry.go "+
			"so registration-time collision rejection is reflected consistently in Execute's dispatch path. "+
			"See https://github.com/elicify-ai/omnipus/issues/278",
			"shared_name", "trusted-result", result.ForLLM)
	}
}
