package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestGetDiscoveryRule_GatedOnManifestCompressed covers finding 1 (GH #657):
// getDiscoveryRule() used to be gated on the unrelated MCP-discovery config
// (which defaults OFF) instead of the tool-manifest system (which defaults
// ON), so a default install rendered no tool-discovery guidance at all. This
// proves the rule is now gated on WithToolDiscovery's single active flag —
// the same flag instance.go wires from cfg.Tools.Manifest.Compressed.
func TestGetDiscoveryRule_GatedOnManifestCompressed(t *testing.T) {
	t.Run("inactive: no rule rendered", func(t *testing.T) {
		cb := NewContextBuilder(t.TempDir()).WithToolDiscovery(false)
		if got := cb.getDiscoveryRule(); got != "" {
			t.Errorf("getDiscoveryRule() with manifest discovery inactive = %q, want empty", got)
		}
	})

	t.Run("active: rule rendered", func(t *testing.T) {
		cb := NewContextBuilder(t.TempDir()).WithToolDiscovery(true)
		got := cb.getDiscoveryRule()
		if got == "" {
			t.Fatal("getDiscoveryRule() with manifest discovery active = \"\", want non-empty")
		}
		if !strings.Contains(got, "### Tool Discovery") {
			t.Errorf("expected an unnumbered '### Tool Discovery' subsection (finding 7); got:\n%s", got)
		}
		// The stale hardcoded ordinal from finding 7 must never reappear.
		if strings.Contains(got, "5. **Tool Discovery**") {
			t.Errorf("rule still hardcodes ordinal '5.' — finding 7 regression; got:\n%s", got)
		}
	})
}

// TestGetDiscoveryRule_NamesLiveInfraTool covers finding 1's second half: the
// rendered rule must name the CURRENT infra discovery tool
// (tools.InfraManifestToolNames(), i.e. "ToolSearch"), and must never name
// either of the retired tool_search_tool_bm25/tool_search_tool_regex
// identifiers that predate the load_tool/ToolSearch unification (GH #657).
func TestGetDiscoveryRule_NamesLiveInfraTool(t *testing.T) {
	cb := NewContextBuilder(t.TempDir()).WithToolDiscovery(true)
	got := cb.getDiscoveryRule()

	for _, name := range tools.InfraManifestToolNames() {
		if !strings.Contains(got, name) {
			t.Errorf("getDiscoveryRule() missing live infra tool name %q; got:\n%s", name, got)
		}
	}

	for _, retired := range []string{"tool_search_tool_bm25", "tool_search_tool_regex"} {
		if strings.Contains(got, retired) {
			t.Errorf("getDiscoveryRule() names retired tool %q (GH #657 regression); got:\n%s", retired, got)
		}
	}
}

// TestWithToolDiscovery_WiredFromManifestCompressed proves the exact wiring
// contract instance.go relies on: WithToolDiscovery(cfg.Tools.Manifest.Compressed)
// — passing the manifest flag straight through, with no MCP-discovery
// dependency of any kind.
func TestWithToolDiscovery_WiredFromManifestCompressed(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())

	// Default-constructed builder: no discovery rule (matches
	// manifestDiscoveryActive's zero value being false).
	if got := cb.getDiscoveryRule(); got != "" {
		t.Errorf("fresh ContextBuilder.getDiscoveryRule() = %q, want empty before WithToolDiscovery is called", got)
	}

	cb.WithToolDiscovery(true)
	if got := cb.getDiscoveryRule(); got == "" {
		t.Error("getDiscoveryRule() empty after WithToolDiscovery(true)")
	}

	cb.WithToolDiscovery(false)
	if got := cb.getDiscoveryRule(); got != "" {
		t.Errorf("getDiscoveryRule() = %q after WithToolDiscovery(false), want empty", got)
	}
}
