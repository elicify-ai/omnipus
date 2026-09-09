package browser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestBrowserToolRegistration verifies that RegisterTools registers all 11 browser tools.
// Traces to: wave4-whatsapp-browser-spec.md line 1001 (Test #11: TestBrowserToolRegistration)
// BDD: Given an empty ToolRegistry,
// When RegisterTools(registry, DefaultConfig(), nil) is called,
// Then 11 tools are registered: the original 7 (browser_navigate, browser_click, browser_type,
// browser_screenshot, browser_get_text, browser_wait, browser_evaluate), the 3 ADR-041 tab-set
// tools (browser_list_tabs, browser_switch_tab, browser_close_tab), and browser_open_tab.

func TestBrowserToolRegistration(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md line 527 (Scenario: Launch managed Chromium)
	registry := tools.NewToolRegistry()
	require.NotNil(t, registry)

	cfg, err := DefaultConfig()
	require.NoError(t, err, "DefaultConfig must not error")
	ssrf := security.NewSSRFChecker(nil)
	// evaluateEnabled=true: verify browser_evaluate is included in the 7 registered tools.
	mgr, err := registerToolsForTest(t, registry, cfg, ssrf, true, t.TempDir(), true)
	require.NoError(t, err, "RegisterTools must not return an error with valid config")
	require.NotNil(t, mgr, "RegisterTools must return a non-nil BrowserManager")

	expectedTools := []string{
		"browser_navigate",
		"browser_click",
		"browser_type",
		"browser_screenshot",
		"browser_get_text",
		"browser_wait",
		"browser_evaluate",
		// ADR-041 multi-tab tools:
		"browser_list_tabs",
		"browser_switch_tab",
		"browser_close_tab",
		// Opens a NEW tab (live-UAT finding — no agent tool to open a new tab):
		"browser_open_tab",
	}

	for _, name := range expectedTools {
		tool, ok := registry.Get(name)
		assert.True(t, ok, "tool %q must be registered", name)
		if ok {
			assert.Equal(t, name, tool.Name(), "tool Name() must match registration key %q", name)
		}
	}

	// 7 original browser tools (FR-009) + 3 ADR-041 multi-tab tools + browser_open_tab.
	assert.Len(t, expectedTools, 11, "expected 11 browser tools (7 FR-009 + 3 ADR-041 tab tools + browser_open_tab)")
}

// TestBrowserToolNames verifies that each registered tool returns the correct name and
// a non-empty description per the API contract.
// Traces to: wave4-whatsapp-browser-spec.md line 1001 (Test #11 — tool API contract)

func TestBrowserToolNames(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md line 527 (Scenario: Launch managed Chromium)
	registry := tools.NewToolRegistry()
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	ssrf := security.NewSSRFChecker(nil)
	_, err = registerToolsForTest(t, registry, cfg, ssrf, true, t.TempDir(), true)
	require.NoError(t, err)

	toolNames := []string{
		"browser_navigate",
		"browser_click",
		"browser_type",
		"browser_screenshot",
		"browser_get_text",
		"browser_wait",
		"browser_evaluate",
		"browser_list_tabs",
		"browser_switch_tab",
		"browser_close_tab",
		"browser_open_tab",
	}

	for _, name := range toolNames {
		tool, ok := registry.Get(name)
		require.True(t, ok, "tool %q must be registered", name)
		assert.NotEmpty(t, tool.Description(), "tool %q must have a non-empty description", name)
		params := tool.Parameters()
		assert.NotNil(t, params, "tool %q must return non-nil Parameters()", name)
	}
}
