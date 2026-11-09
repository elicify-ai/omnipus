// Issue #638 fix-round regression: a literal MCP header value under
// tools.mcp.servers.<name>.headers must never leave get_config — not on a
// direct read, not on a deeper read, and not on an ANCESTOR read (key
// "tools.mcp" or "tools").
//
// Specification source: the config read-policy table's own contract for the
// tools.mcp key (config.go, blockedConfigKeys entry "tools.mcp",
// ReadOKReason): "the configured servers ... the list is readable while its
// secrets are not" — and issue #638's intent: no literal header secret ever
// leaves get_config on its way to model context. Expected values derive from
// that contract, never from the current implementation's output.

package systools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// ancestorLeakSecret is unique to this file: if it ever appears in a
// get_config result, a literal MCP header value rode out of the tool.
const ancestorLeakSecret = "gwsec-638-ancestor-leak-sentinel"

// seedMCPLiteralHeader seeds one MCP server whose Authorization header holds
// the literal secret — the shape a pre-#638 install, a hand edit, or a
// pre-guard PUT /api/v1/config write leaves in config.json.
func seedMCPLiteralHeader(cfg *config.Config) {
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"remote": {
			Enabled: true,
			Type:    "http",
			URL:     "https://127.0.0.1:1/mcp",
			Headers: map[string]string{
				"Authorization": "Bearer " + ancestorLeakSecret,
			},
		},
	}
}

// TestConfigGet_MCPLiteralHeader_AbsentFromAncestorReads is the F1 regression
// (round-2 review, High): get_config with an ancestor key — "tools.mcp" or
// "tools" — must succeed (the policy table's ReadOKReason invites the read)
// and must return the servers list WITHOUT the literal header value anywhere
// in the result, exactly like the deeper reads already do.
func TestConfigGet_MCPLiteralHeader_AbsentFromAncestorReads(t *testing.T) {
	for _, key := range []string{"tools.mcp", "tools"} {
		t.Run(key, func(t *testing.T) {
			deps, cfg := newTestDeps(t)
			seedMCPLiteralHeader(cfg)
			tool := systools.NewConfigGetTool(deps)

			result := tool.Execute(context.Background(), map[string]any{"key": key})

			if result.IsError {
				t.Fatalf("get_config with key %q must succeed (ReadOKReason invites the read), got error: %s",
					key, result.ForLLM)
			}
			if strings.Contains(result.ForLLM, ancestorLeakSecret) {
				t.Fatalf("F1 #638: get_config with key %q returned the literal MCP header secret %q — "+
					"ancestor reads must redact header values the same way deeper reads do.\nresult: %s",
					key, ancestorLeakSecret, result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, "remote") {
				t.Errorf("get_config with key %q must keep the server list readable "+
					"(ReadOKReason: 'the list is readable'), but the server name is missing from: %s",
					key, result.ForLLM)
			}
		})
	}
}

// TestConfigGet_MCPLiteralHeader_AbsentFromDeeperReads_Control pins the
// already-correct read shapes (tools.mcp.servers and below). It is the
// instrument check for the ancestor test above: it proves the seeding and
// assertion machinery can see the secret when a read shape leaks, so an
// ancestor-run failure is the missing guard and not a broken test.
func TestConfigGet_MCPLiteralHeader_AbsentFromDeeperReads_Control(t *testing.T) {
	for _, key := range []string{
		"tools.mcp.servers",
		"tools.mcp.servers.remote",
		"tools.mcp.servers.remote.headers",
		"tools.mcp.servers.remote.headers.Authorization",
	} {
		t.Run(key, func(t *testing.T) {
			deps, cfg := newTestDeps(t)
			seedMCPLiteralHeader(cfg)
			tool := systools.NewConfigGetTool(deps)

			result := tool.Execute(context.Background(), map[string]any{"key": key})
			if result.IsError {
				t.Fatalf("get_config with key %q must succeed, got error: %s", key, result.ForLLM)
			}
			if strings.Contains(result.ForLLM, ancestorLeakSecret) {
				t.Errorf("get_config with key %q leaked the literal header secret — "+
					"a shape that was already guarded has regressed.\nresult: %s", key, result.ForLLM)
			}
		})
	}
}

// headerRefSentinel is a credential-store key name unique to this test: if it
// appears in a get_config result, an MCP header ref rode out of the tool.
const headerRefSentinel = "mcp_remote_header_gwsec906RefSentinel"

// TestConfigGet_MCPHeaderRefs_NotReturned pins ARCH-906 change 4: header_refs
// (credential-store indirection, internal-only) must not leave get_config on
// any read shape, so the agent read path agrees with GET /api/v1/config,
// which strips them. Section reads keep the server readable without the
// refs; a read that addresses the refs directly is refused.
func TestConfigGet_MCPHeaderRefs_NotReturned(t *testing.T) {
	seed := func(cfg *config.Config) {
		cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
			"remote": {
				Enabled:    true,
				Type:       "http",
				URL:        "https://127.0.0.1:1/mcp",
				HeaderRefs: map[string]string{"Authorization": headerRefSentinel},
			},
		}
	}
	for _, key := range []string{"tools", "tools.mcp", "tools.mcp.servers", "tools.mcp.servers.remote"} {
		t.Run(key, func(t *testing.T) {
			deps, cfg := newTestDeps(t)
			seed(cfg)
			result := systools.NewConfigGetTool(deps).Execute(context.Background(), map[string]any{"key": key})
			if result.IsError {
				t.Fatalf("get_config %q must succeed, got error: %s", key, result.ForLLM)
			}
			if strings.Contains(result.ForLLM, "header_refs") || strings.Contains(result.ForLLM, headerRefSentinel) {
				t.Fatalf("ARCH-906: get_config %q returned header_refs.\nresult: %s", key, result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, "remote") && key != "tools.mcp.servers.remote" {
				t.Errorf("get_config %q must keep the server list readable: %s", key, result.ForLLM)
			}
			if key == "tools.mcp.servers.remote" && !strings.Contains(result.ForLLM, "127.0.0.1") {
				t.Errorf("get_config %q must keep the server entry readable: %s", key, result.ForLLM)
			}
		})
	}
	for _, key := range []string{"tools.mcp.servers.remote.header_refs", "tools.mcp.servers.remote.header_refs.Authorization"} {
		t.Run(key, func(t *testing.T) {
			deps, cfg := newTestDeps(t)
			seed(cfg)
			result := systools.NewConfigGetTool(deps).Execute(context.Background(), map[string]any{"key": key})
			if !result.IsError {
				t.Errorf("ARCH-906: a direct read of header_refs (%q) must be refused, got: %s", key, result.ForLLM)
			}
			if strings.Contains(result.ForLLM, headerRefSentinel) {
				t.Fatalf("ARCH-906: get_config %q returned the ref name.\nresult: %s", key, result.ForLLM)
			}
		})
	}
}

// TestConfigGet_MCPAncestorRead_ReturnsRequestedSubtree is the SEC906 fix-round
// F3 regression (security review 45e960009): get_config's value is the subtree
// AT the requested key. An ancestor read ("tools", "tools.mcp") must return
// that ancestor, with its sibling settings and the nested mcp/servers levels,
// and only redact the header values inside it; it must never replace the
// answer with the deeper servers map. Derived from the reviewer's reproduction
// (sec906-review-probes/sysagent_proofs_test.go) and get_config's key/value
// contract.
func TestConfigGet_MCPAncestorRead_ReturnsRequestedSubtree(t *testing.T) {
	cases := []struct {
		key     string
		sibling string // a non-server field that lives next to the MCP level
	}{
		{key: "tools", sibling: "exec"},
		{key: "tools.mcp", sibling: "discovery"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			deps, cfg := newTestDeps(t)
			seedMCPLiteralHeader(cfg)

			result := systools.NewConfigGetTool(deps).Execute(context.Background(), map[string]any{"key": tc.key})

			if result.IsError {
				t.Fatalf("read %s: %s", tc.key, result.ForLLM)
			}
			if strings.Contains(result.ForLLM, ancestorLeakSecret) {
				t.Fatalf("secret escaped from %s read: %s", tc.key, result.ForLLM)
			}
			got := parseSuccess(t, result.ForLLM)
			value, ok := got["value"].(map[string]any)
			if !ok {
				t.Fatalf("value must be an object: %s", result.ForLLM)
			}
			if _, present := value[tc.sibling]; !present {
				t.Fatalf("%s read lost its sibling field %q; the value is not the requested subtree: %s",
					tc.key, tc.sibling, result.ForLLM)
			}
			mcp := value
			if tc.key == "tools" {
				mcp, ok = value["mcp"].(map[string]any)
				if !ok {
					t.Fatalf("tools read lost the mcp child: %s", result.ForLLM)
				}
			}
			servers, ok := mcp["servers"].(map[string]any)
			if !ok {
				t.Fatalf("%s read lost the servers level: %s", tc.key, result.ForLLM)
			}
			remote, ok := servers["remote"].(map[string]any)
			if !ok {
				t.Fatalf("server remote must remain visible: %s", result.ForLLM)
			}
			headers, ok := remote["headers"].(map[string]any)
			if !ok {
				t.Fatalf("server remote must keep its header names: %s", result.ForLLM)
			}
			if headers["Authorization"] != "[REDACTED]" {
				t.Fatalf("Authorization must be served redacted, got %v", headers["Authorization"])
			}
		})
	}
}
