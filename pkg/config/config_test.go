package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAgentConfig_Clone_Independence verifies that Clone returns a fully
// independent deep copy — mutations to the original do not affect the clone
// and vice versa.
//
// Traces to: Blocker 2 — WithConfig uses Clone for full-config rollback.
func TestAgentConfig_Clone_Independence(t *testing.T) {
	orig := DefaultConfig()
	orig.Agents.List = []AgentConfig{
		{ID: "agent-1", Name: "Original"},
	}

	clone, err := orig.Clone()
	if err != nil {
		t.Fatalf("Clone() returned error: %v", err)
	}
	if clone == nil {
		t.Fatal("Clone() returned nil")
	}

	// Mutation 1: append to orig.Agents.List — clone must not see it.
	orig.Agents.List = append(orig.Agents.List, AgentConfig{ID: "agent-2", Name: "New"})
	if len(clone.Agents.List) != 1 {
		t.Errorf("clone.Agents.List length = %d after appending to original; want 1", len(clone.Agents.List))
	}

	// Mutation 2: change a string field on the original — clone must keep old value.
	orig.Agents.List[0].Name = "Changed"
	if clone.Agents.List[0].Name != "Original" {
		t.Errorf("clone.Agents.List[0].Name = %q after mutating original; want Original", clone.Agents.List[0].Name)
	}

	// Mutation 3: mutate the clone — original must not be affected.
	clone.Gateway.Port = 9999
	if orig.Gateway.Port == 9999 {
		t.Error("mutating clone.Gateway.Port affected the original")
	}
}

func TestAgentModelConfig_UnmarshalString(t *testing.T) {
	var m AgentModelConfig
	if err := json.Unmarshal([]byte(`"gpt-4"`), &m); err != nil {
		t.Fatalf("unmarshal string: %v", err)
	}
	if m.Primary != "gpt-4" {
		t.Errorf("Primary = %q, want 'gpt-4'", m.Primary)
	}
	if m.Fallbacks != nil {
		t.Errorf("Fallbacks = %v, want nil", m.Fallbacks)
	}
}

func TestAgentModelConfig_UnmarshalObject(t *testing.T) {
	var m AgentModelConfig
	data := `{"primary": "claude-opus", "fallbacks": ["gpt-4o-mini", "haiku"]}`
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatalf("unmarshal object: %v", err)
	}
	if m.Primary != "claude-opus" {
		t.Errorf("Primary = %q, want 'claude-opus'", m.Primary)
	}
	if len(m.Fallbacks) != 2 {
		t.Fatalf("Fallbacks len = %d, want 2", len(m.Fallbacks))
	}
	if m.Fallbacks[0] != "gpt-4o-mini" || m.Fallbacks[1] != "haiku" {
		t.Errorf("Fallbacks = %v", m.Fallbacks)
	}
}

func TestAgentModelConfig_MarshalString(t *testing.T) {
	m := AgentModelConfig{Primary: "gpt-4"}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `"gpt-4"` {
		t.Errorf("marshal = %s, want '\"gpt-4\"'", string(data))
	}
}

func TestAgentModelConfig_MarshalObject(t *testing.T) {
	m := AgentModelConfig{Primary: "claude-opus", Fallbacks: []string{"haiku"}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var result map[string]any
	json.Unmarshal(data, &result)
	if result["primary"] != "claude-opus" {
		t.Errorf("primary = %v", result["primary"])
	}
}

// TestAgentConfig_FullParse_AgentsListNeverUnmarshals is a pinning test for
// Bug 1's fix (AgentsConfig.List json:"-", see its doc comment on config.go):
// a raw json.Unmarshal of a config.json-shaped payload carrying an
// "agents.list" array must NOT populate cfg.Agents.List — the field is
// structurally invisible to JSON now, both marshal and unmarshal, so no
// config-write path can ever re-inject the roster (SaveConfig) and no
// legacy-load path can ever read it back into this typed field either
// (legacy content is instead detected/dropped from the raw file bytes by
// legacy_agents_list.go's stripAgentsListOnDisk). Every OTHER field in the
// same payload — agents.defaults, bindings, session — must still parse
// normally; only "list" is inert.
//
// Formerly named TestAgentConfig_FullParse, when this exact payload asserted
// the opposite (that Agents.List DID populate with 2 agents) — that was the
// pre-Bug-1-fix behavior this change deliberately reverses.
func TestAgentConfig_FullParse_AgentsListNeverUnmarshals(t *testing.T) {
	jsonData := `{
		"agents": {
			"defaults": {
				"workspace": "~/.omnipus/workspace",
				"model": "glm-4.7",
				"max_tokens": 8192,
				"max_tool_iterations": 20
			},
			"list": [
				{
					"id": "sales",
					"default": true,
					"name": "Sales Bot",
					"model": "gpt-4"
				},
				{
					"id": "support",
					"name": "Support Bot",
					"model": {
						"primary": "claude-opus",
						"fallbacks": ["haiku"]
					},
					"subagents": {
						"allow_agents": ["sales"]
					}
				}
			]
		},
		"bindings": [
			{
				"agent_id": "support",
				"match": {
					"channel": "telegram",
					"account_id": "*",
					"peer": {"kind": "direct", "id": "user123"}
				}
			}
		],
		"session": {
			"dm_scope": "per-peer",
			"identity_links": {
				"john": ["telegram:123", "discord:john#1234"]
			}
		}
	}`

	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(jsonData), cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// The heart of Bug 1's fix: json:"-" means this raw unmarshal must never
	// populate the field, regardless of what the payload's "list" key holds.
	if len(cfg.Agents.List) != 0 {
		t.Fatalf("agents.list len = %d, want 0 — AgentsConfig.List must be json:\"-\" "+
			"(structurally non-serializable); got %+v", len(cfg.Agents.List), cfg.Agents.List)
	}

	// agents.defaults, a genuine SETTING (not part of the roster), must still
	// parse normally alongside the now-inert list key.
	if cfg.Agents.Defaults.Home != "~/.omnipus/workspace" {
		t.Errorf("Agents.Defaults.Home = %q", cfg.Agents.Defaults.Home)
	}
	if cfg.Agents.Defaults.MaxTokens != 8192 {
		t.Errorf("Agents.Defaults.MaxTokens = %d", cfg.Agents.Defaults.MaxTokens)
	}

	if len(cfg.Bindings) != 1 {
		t.Fatalf("bindings len = %d, want 1", len(cfg.Bindings))
	}
	binding := cfg.Bindings[0]
	if binding.AgentID != "support" || binding.Match.Channel != "telegram" {
		t.Errorf("binding = %+v", binding)
	}
	if binding.Match.Peer == nil || binding.Match.Peer.Kind != "direct" || binding.Match.Peer.ID != "user123" {
		t.Errorf("binding.Match.Peer = %+v", binding.Match.Peer)
	}

	if cfg.Session.DMScope != "per-peer" {
		t.Errorf("Session.DMScope = %q", cfg.Session.DMScope)
	}
	if len(cfg.Session.IdentityLinks) != 1 {
		t.Errorf("Session.IdentityLinks = %v", cfg.Session.IdentityLinks)
	}
	links := cfg.Session.IdentityLinks["john"]
	if len(links) != 2 {
		t.Errorf("john links = %v", links)
	}
}

func TestConfig_BackwardCompat_NoAgentsList(t *testing.T) {
	jsonData := `{
		"agents": {
			"defaults": {
				"workspace": "~/.omnipus/workspace",
				"model": "glm-4.7",
				"max_tokens": 8192,
				"max_tool_iterations": 20
			}
		}
	}`

	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(jsonData), cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(cfg.Agents.List) != 0 {
		t.Errorf("agents.list should be empty for backward compat, got %d", len(cfg.Agents.List))
	}
	if len(cfg.Bindings) != 0 {
		t.Errorf("bindings should be empty, got %d", len(cfg.Bindings))
	}
}

// TestDefaultConfig_WorkspacePath verifies workspace path is correctly set
func TestDefaultConfig_WorkspacePath(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.Home == "" {
		t.Error("Workspace should not be empty")
	}
}

// TestDefaultConfig_MaxTokens verifies max tokens has default value
func TestDefaultConfig_MaxTokens(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.MaxTokens == 0 {
		t.Error("MaxTokens should not be zero")
	}
}

// TestDefaultConfig_MaxToolIterations verifies max tool iterations has default value
func TestDefaultConfig_MaxToolIterations(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.MaxToolIterations == 0 {
		t.Error("MaxToolIterations should not be zero")
	}
}

// TestDefaultConfig_Temperature verifies temperature has default value
func TestDefaultConfig_Temperature(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.Temperature != nil {
		t.Error("Temperature should be nil when not provided")
	}
}

// TestDefaultConfig_BrowserLiveView verifies the ADR-038 live interactive
// browser panel and its take-control capability are both enabled by default,
// matching every other standard built-in tool (exec/web/cron default
// Enabled=true) — the operator kill-switches
// (tools.browser.live_view_enabled / tools.browser.take_control_enabled)
// default ON, not OFF.
// Traces to: docs/internal/architecture/ADR-038-live-interactive-browser-panel.md
func TestDefaultConfig_BrowserLiveView(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.Tools.Browser.LiveViewEnabled {
		t.Error("Tools.Browser.LiveViewEnabled should default to true (ADR-038)")
	}
	if !cfg.Tools.Browser.TakeControlEnabled {
		t.Error("Tools.Browser.TakeControlEnabled should default to true (ADR-038 D6)")
	}
}

// TestDefaultConfig_Gateway verifies gateway defaults
func TestDefaultConfig_Gateway(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Gateway.Host != "127.0.0.1" {
		t.Error("Gateway host should have default value")
	}
	if cfg.Gateway.Port == 0 {
		t.Error("Gateway port should have default value")
	}
	// FR-106: hot_reload must default to true so fresh installs apply config
	// changes without a restart. Operators who set hot_reload:false in config.json
	// retain the old behavior (JSON value wins over the default).
	if !cfg.Gateway.HotReload {
		t.Error("Gateway hot reload should be enabled by default (FR-106)")
	}
}

// TestDefaultConfig_Channels verifies channels are disabled by default
func TestDefaultConfig_Channels(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Channels["telegram"].Enabled {
		t.Error("Telegram should be disabled by default")
	}
	if cfg.Channels["discord"].Enabled {
		t.Error("Discord should be disabled by default")
	}
	if cfg.Channels["slack"].Enabled {
		t.Error("Slack should be disabled by default")
	}
	if cfg.Channels["matrix"].Enabled {
		t.Error("Matrix should be disabled by default")
	}
}

// TestDefaultConfig_WebTools verifies web tools config
func TestDefaultConfig_WebTools(t *testing.T) {
	cfg := DefaultConfig()

	// Verify web tools defaults
	if cfg.Tools.Web.Brave.MaxResults != 5 {
		t.Error("Expected Brave MaxResults 5, got ", cfg.Tools.Web.Brave.MaxResults)
	}
	if cfg.Tools.Web.Brave.APIKeyRef != "" {
		t.Error("Brave APIKeyRef should be empty by default")
	}
	if cfg.Tools.Web.DuckDuckGo.MaxResults != 5 {
		t.Error("Expected DuckDuckGo MaxResults 5, got ", cfg.Tools.Web.DuckDuckGo.MaxResults)
	}
}

func TestSaveConfig_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits are not enforced on Windows")
	}

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	cfg := DefaultConfig()
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("config file has permission %04o, want 0600", perm)
	}
}

func TestSaveConfig_IncludesEmptyLegacyModelField(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	cfg := DefaultConfig()
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	if !strings.Contains(string(data), `"model_name": ""`) {
		t.Fatalf("saved config should include empty legacy model_name field, got: %s", string(data))
	}
}

func TestSaveConfig_PreservesDisabledTelegramPlaceholder(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	cfg := DefaultConfig()
	{
		inst := cfg.Channels["telegram"]
		inst.Placeholder.Enabled = false
		cfg.Channels["telegram"] = inst
	}

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(data), `"placeholder": {`) {
		t.Fatalf("saved config should include telegram placeholder config, got: %s", string(data))
	}
	if !strings.Contains(string(data), `"enabled": false`) {
		t.Fatalf("saved config should persist placeholder.enabled=false, got: %s", string(data))
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if loaded.Channels["telegram"].Placeholder.Enabled {
		t.Fatal("telegram placeholder should remain disabled after SaveConfig/LoadConfig round-trip")
	}
}

// TestSaveConfig_FiltersVirtualModels verifies that SaveConfig does not write
// virtual models (generated by expandMultiKeyModels) to the config file.
func TestSaveConfig_FiltersVirtualModels(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	cfg := DefaultConfig()

	// Manually add a virtual model to Providers (simulating what expandMultiKeyModels does)
	primaryModel := &ModelConfig{
		ModelName: "gpt-4",
		Model:     "openai/gpt-4o",
		APIKeyRef: "OPENAI_API_KEY",
	}
	virtualModel := &ModelConfig{
		ModelName: "gpt-4__key_1",
		Model:     "openai/gpt-4o",
		APIKeyRef: "OPENAI_API_KEY_2",
		isVirtual: true,
	}
	cfg.Providers = []*ModelConfig{primaryModel, virtualModel}

	// SaveConfig should filter out virtual models
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	// Reload and verify
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Should only have the primary model, not the virtual one
	if len(reloaded.Providers) != 1 {
		t.Fatalf("expected 1 model after reload, got %d", len(reloaded.Providers))
	}

	if reloaded.Providers[0].ModelName != "gpt-4" {
		t.Errorf("expected model_name 'gpt-4', got %q", reloaded.Providers[0].ModelName)
	}

	// Verify virtual model was not persisted
	for _, m := range reloaded.Providers {
		if m.ModelName == "gpt-4__key_1" {
			t.Errorf("virtual model gpt-4__key_1 should not have been saved")
		}
	}

	// Verify the saved file does not contain the virtual model name
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if strings.Contains(string(data), "gpt-4__key_1") {
		t.Errorf("saved config should not contain virtual model name 'gpt-4__key_1'")
	}
}

// TestConfig_Complete verifies all config fields are set
func TestConfig_Complete(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.Home == "" {
		t.Error("Workspace should not be empty")
	}
	if cfg.Agents.Defaults.Temperature != nil {
		t.Error("Temperature should be nil when not provided")
	}
	if cfg.Agents.Defaults.MaxTokens == 0 {
		t.Error("MaxTokens should not be zero")
	}
	if cfg.Agents.Defaults.MaxToolIterations == 0 {
		t.Error("MaxToolIterations should not be zero")
	}
	if cfg.Gateway.Host != "127.0.0.1" {
		t.Error("Gateway host should have default value")
	}
	if cfg.Gateway.Port == 0 {
		t.Error("Gateway port should have default value")
	}
}

func TestDefaultConfig_WebPreferNativeEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Tools.Web.PreferNative {
		t.Fatal("DefaultConfig().Tools.Web.PreferNative should be true")
	}
}

func TestDefaultConfig_ToolFeedbackDisabled(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Agents.Defaults.ToolFeedback.Enabled {
		t.Fatal("DefaultConfig().Agents.Defaults.ToolFeedback.Enabled should be false")
	}
}

func TestLoadConfig_ToolFeedbackDefaultsFalseWhenUnset(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(
		configPath,
		[]byte(`{"version":1,"agents":{"defaults":{"workspace":"./workspace"}}}`),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Agents.Defaults.ToolFeedback.Enabled {
		t.Fatal("agents.defaults.tool_feedback.enabled should remain false when unset in config file")
	}
}

func TestLoadConfig_WebPreferNativeDefaultsTrueWhenUnset(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"version":1,"tools":{"web":{"enabled":true}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if !cfg.Tools.Web.PreferNative {
		t.Fatal("PreferNative should remain true when unset in config file")
	}
}

func TestLoadConfig_WebPreferNativeCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	rawCfg := []byte(`{"version":1,"tools":{"web":{"prefer_native":false}}}`)
	if err := os.WriteFile(configPath, rawCfg, 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Tools.Web.PreferNative {
		t.Fatal("PreferNative should be false when disabled in config file")
	}
}

func TestDefaultConfig_FilterSensitiveDataEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Tools.FilterSensitiveData {
		t.Fatal("DefaultConfig().Tools.FilterSensitiveData should be true")
	}
}

func TestDefaultConfig_FilterMinLength(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Tools.FilterMinLength != 8 {
		t.Fatalf("DefaultConfig().Tools.FilterMinLength = %d, want 8", cfg.Tools.FilterMinLength)
	}
}

func TestToolsConfig_GetFilterMinLength(t *testing.T) {
	tests := []struct {
		name     string
		minLen   int
		expected int
	}{
		{"zero returns default", 0, 8},
		{"negative returns default", -1, 8},
		{"positive returns value", 16, 16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ToolsConfig{FilterMinLength: tt.minLen}
			if got := cfg.GetFilterMinLength(); got != tt.expected {
				t.Errorf("GetFilterMinLength() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDefaultConfig_CronAllowCommandEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Tools.Cron.AllowCommand {
		t.Fatal("DefaultConfig().Tools.Cron.AllowCommand should be true")
	}
}

func TestDefaultConfig_HooksDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Hooks.Enabled {
		t.Fatal("DefaultConfig().Hooks.Enabled should be true")
	}
	if cfg.Hooks.Defaults.ObserverTimeoutMS != 500 {
		t.Fatalf("ObserverTimeoutMS = %d, want 500", cfg.Hooks.Defaults.ObserverTimeoutMS)
	}
	if cfg.Hooks.Defaults.InterceptorTimeoutMS != 5000 {
		t.Fatalf("InterceptorTimeoutMS = %d, want 5000", cfg.Hooks.Defaults.InterceptorTimeoutMS)
	}
	if cfg.Hooks.Defaults.ApprovalTimeoutMS != 60000 {
		t.Fatalf("ApprovalTimeoutMS = %d, want 60000", cfg.Hooks.Defaults.ApprovalTimeoutMS)
	}
}

func TestDefaultConfig_LogLevel(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Gateway.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want \"fatal\"", cfg.Gateway.LogLevel)
	}
}

func TestLoadConfig_CronAllowCommandDefaultsTrueWhenUnset(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(
		configPath,
		[]byte(`{"version":1,"tools":{"cron":{"exec_timeout_minutes":5}}}`),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if !cfg.Tools.Cron.AllowCommand {
		t.Fatal("tools.cron.allow_command should remain true when unset in config file")
	}
}

func TestLoadConfig_WebToolsProxy(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{
  "version": 1,
  "agents": {"defaults":{"workspace":"./workspace","model_name":"gpt4","max_tokens":8192,"max_tool_iterations":20}},
  "providers": [{"model_name":"gpt4","model":"openai/gpt-5.4","api_key_ref":"OPENAI_API_KEY"}],
  "tools": {"web":{"proxy":"http://127.0.0.1:7890"}}
}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Tools.Web.Proxy != "http://127.0.0.1:7890" {
		t.Fatalf("Tools.Web.Proxy = %q, want %q", cfg.Tools.Web.Proxy, "http://127.0.0.1:7890")
	}
}

// TestLoadConfig_BrowserExecPath verifies tools.browser.exec_path round
// trips through LoadConfig into cfg.Tools.Browser.ExecPath. This field is
// consumed downstream by pkg/agent/loop.go's registerSharedTools, which
// copies cfg.Tools.Browser.ExecPath into browser.BrowserConfig.ExecPath
// (only when non-empty, mirroring the other optional overrides in that same
// copy block) before calling browser.RegisterTools — that copy is exercised
// end-to-end by pkg/tools/browser's own manager tests, not here, since
// registerSharedTools has no seam for a config-only unit test.
func TestLoadConfig_BrowserExecPath(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{"version":1,"tools":{"browser":{"exec_path":"/opt/custom-chromium/chrome"}}}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Tools.Browser.ExecPath != "/opt/custom-chromium/chrome" {
		t.Fatalf("Tools.Browser.ExecPath = %q, want %q", cfg.Tools.Browser.ExecPath, "/opt/custom-chromium/chrome")
	}
}

// TestLoadConfig_BrowserExecPathDefaultsEmpty verifies that omitting
// tools.browser.exec_path leaves it empty — the auto-discover default (see
// pkg/tools/browser.BrowserManager.resolveExecPath) — rather than some
// zero-value that could be mistaken for an explicit override.
func TestLoadConfig_BrowserExecPathDefaultsEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{"version":1,"tools":{"browser":{"max_tabs":3}}}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Tools.Browser.ExecPath != "" {
		t.Fatalf("Tools.Browser.ExecPath = %q, want empty when unset", cfg.Tools.Browser.ExecPath)
	}
}

// TestDefaultConfig_BrowserADR052SecurityDefaults (ADR-052 D2 / SEC-ADR052-002)
// asserts that the fresh-install defaults for the two new ADR-052 fields
// are FALSE — operators keep $PATH Chrome as the winning source by default
// (D2) and the resolver REFUSES to launch a $PATH Chrome (SEC-ADR052-002).
// Both defaults are part of the ADR-052 security posture: flipping either
// to true in a fresh seed would silently change browser provenance
// expectations on every install. A regression here is a silent downgrade
// of the ADR-052 contract, not a benign default tweak.
//
// Traces to: docs/internal/architecture/ADR-052-native-cross-platform-and-bundled-Chrome.md
func TestDefaultConfig_BrowserADR052SecurityDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Tools.Browser.PreferPackaged {
		t.Errorf("Tools.Browser.PreferPackaged should default to false (ADR-052 D2: " +
			"operators keep $PATH Chrome as the winning source by default; prefer_packaged is " +
			"the explicit opt-in for fleets that want reproducibility)")
	}
	if cfg.Tools.Browser.TrustPathChrome {
		t.Errorf("Tools.Browser.TrustPathChrome should default to false (ADR-052 SEC-ADR052-002: " +
			"a $PATH Chrome is recorded but refused; trust_path_chrome is the explicit opt-in for " +
			"operators with a deliberate custom $PATH Chrome)")
	}
}

// TestLoadConfig_BrowserPreferPackaged verifies that tools.browser.prefer_packaged
// round-trips through LoadConfig into cfg.Tools.Browser.PreferPackaged.
// The runtime resolver at pkg/tools/browser/exec_resolver.go reads this
// field to decide whether the verified package Chrome outranks a $PATH
// Chrome on a non-TrustPathChrome install — its correctness depends on
// this loader path staying wired.
func TestLoadConfig_BrowserPreferPackaged(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{"version":1,"tools":{"browser":{"prefer_packaged":true}}}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if !cfg.Tools.Browser.PreferPackaged {
		t.Fatalf("Tools.Browser.PreferPackaged = %v, want true", cfg.Tools.Browser.PreferPackaged)
	}

	// Also verify the env-var binding OMNIPUS_TOOLS_BROWSER_PREFER_PACKAGED=true
	// lands correctly. We snapshot and restore to keep the test hermetic.
	t.Setenv("OMNIPUS_TOOLS_BROWSER_PREFER_PACKAGED", "true")
	cfg2, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() with env var error: %v", err)
	}
	if !cfg2.Tools.Browser.PreferPackaged {
		t.Errorf("Tools.Browser.PreferPackaged = %v under OMNIPUS_TOOLS_BROWSER_PREFER_PACKAGED=true, want true",
			cfg2.Tools.Browser.PreferPackaged)
	}
}

// TestLoadConfig_BrowserPreferPackagedDefaultsFalse mirrors the
// ExecPathDefaultsEmpty pattern: omitting the JSON key leaves the seeded
// default in place. Without this test a regression that drops the seed
// (or coerces false→true) could slip past LoadConfig-level coverage.
func TestLoadConfig_BrowserPreferPackagedDefaultsFalse(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{"version":1,"tools":{"browser":{"max_tabs":3}}}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}

	// Pin the env var false so a CI runner carrying OMNIPUS_TOOLS_BROWSER_PREFER_PACKAGED=true
	// from a prior test cannot mask a regression in the loader.
	t.Setenv("OMNIPUS_TOOLS_BROWSER_PREFER_PACKAGED", "false")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Tools.Browser.PreferPackaged {
		t.Fatalf("Tools.Browser.PreferPackaged = %v when omitted from JSON + env false, want false",
			cfg.Tools.Browser.PreferPackaged)
	}
}

// TestLoadConfig_BrowserTrustPathChrome verifies the SEC-ADR052-002 field
// round-trips through LoadConfig and binds to OMNIPUS_TOOLS_BROWSER_TRUST_PATH_CHROME.
// The runtime resolver (pkg/tools/browser/exec_resolver.go) reads this
// field to decide whether to honor a $PATH Chrome or refuse it (WARN-BROWSER-007).
func TestLoadConfig_BrowserTrustPathChrome(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{"version":1,"tools":{"browser":{"trust_path_chrome":true}}}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if !cfg.Tools.Browser.TrustPathChrome {
		t.Fatalf("Tools.Browser.TrustPathChrome = %v, want true", cfg.Tools.Browser.TrustPathChrome)
	}

	t.Setenv("OMNIPUS_TOOLS_BROWSER_TRUST_PATH_CHROME", "true")
	cfg2, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() with env var error: %v", err)
	}
	if !cfg2.Tools.Browser.TrustPathChrome {
		t.Errorf("Tools.Browser.TrustPathChrome = %v under OMNIPUS_TOOLS_BROWSER_TRUST_PATH_CHROME=true, want true",
			cfg2.Tools.Browser.TrustPathChrome)
	}
}

// TestLoadConfig_BrowserTrustPathChromeDefaultsFalse mirrors the
// PreferPackagedDefaultsFalse test for trust_path_chrome.
func TestLoadConfig_BrowserTrustPathChromeDefaultsFalse(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{"version":1,"tools":{"browser":{"max_tabs":3}}}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}

	t.Setenv("OMNIPUS_TOOLS_BROWSER_TRUST_PATH_CHROME", "false")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Tools.Browser.TrustPathChrome {
		t.Fatalf("Tools.Browser.TrustPathChrome = %v when omitted from JSON + env false, want false",
			cfg.Tools.Browser.TrustPathChrome)
	}
}

func TestLoadConfig_HooksProcessConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{
  "version": 1,
  "hooks": {
    "processes": {
      "review-gate": {
        "enabled": true,
        "transport": "stdio",
        "command": ["uvx", "omnipus-hook-reviewer"],
        "dir": "/tmp/hooks",
        "env": {
          "HOOK_MODE": "rewrite"
        },
        "observe": ["turn_start", "turn_end"],
        "intercept": ["before_tool", "approve_tool"]
      }
    },
    "builtins": {
      "audit": {
        "enabled": true,
        "priority": 5,
        "config": {
          "label": "audit"
        }
      }
    }
  }
}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	processCfg, ok := cfg.Hooks.Processes["review-gate"]
	if !ok {
		t.Fatal("expected review-gate process hook")
	}
	if !processCfg.Enabled {
		t.Fatal("expected review-gate process hook to be enabled")
	}
	if processCfg.Transport != "stdio" {
		t.Fatalf("Transport = %q, want stdio", processCfg.Transport)
	}
	if len(processCfg.Command) != 2 || processCfg.Command[0] != "uvx" {
		t.Fatalf("Command = %v", processCfg.Command)
	}
	if processCfg.Dir != "/tmp/hooks" {
		t.Fatalf("Dir = %q, want /tmp/hooks", processCfg.Dir)
	}
	if processCfg.Env["HOOK_MODE"] != "rewrite" {
		t.Fatalf("HOOK_MODE = %q, want rewrite", processCfg.Env["HOOK_MODE"])
	}
	if len(processCfg.Observe) != 2 || processCfg.Observe[1] != "turn_end" {
		t.Fatalf("Observe = %v", processCfg.Observe)
	}
	if len(processCfg.Intercept) != 2 || processCfg.Intercept[1] != "approve_tool" {
		t.Fatalf("Intercept = %v", processCfg.Intercept)
	}

	builtinCfg, ok := cfg.Hooks.Builtins["audit"]
	if !ok {
		t.Fatal("expected audit builtin hook")
	}
	if !builtinCfg.Enabled {
		t.Fatal("expected audit builtin hook to be enabled")
	}
	if builtinCfg.Priority != 5 {
		t.Fatalf("Priority = %d, want 5", builtinCfg.Priority)
	}
	if !strings.Contains(string(builtinCfg.Config), `"audit"`) {
		t.Fatalf("Config = %s", string(builtinCfg.Config))
	}
	if cfg.Hooks.Defaults.ApprovalTimeoutMS != 60000 {
		t.Fatalf("ApprovalTimeoutMS = %d, want 60000", cfg.Hooks.Defaults.ApprovalTimeoutMS)
	}
}

// TestDefaultConfig_DMScope verifies the default dm_scope value
func TestDefaultConfig_DMScope(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Session.DMScope != "per-channel-peer" {
		t.Errorf("Session.DMScope = %q, want 'per-channel-peer'", cfg.Session.DMScope)
	}
}

func TestDefaultConfig_WorkspacePath_Default(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", "")

	var fakeHome string
	if runtime.GOOS == "windows" {
		fakeHome = `C:\tmp\home`
		t.Setenv("USERPROFILE", fakeHome)
	} else {
		fakeHome = "/tmp/home"
		t.Setenv("HOME", fakeHome)
	}

	cfg := DefaultConfig()
	want := filepath.Join(fakeHome, ".omnipus", "workspace")

	if cfg.Agents.Defaults.Home != want {
		t.Errorf("Default workspace path = %q, want %q", cfg.Agents.Defaults.Home, want)
	}
}

func TestDefaultConfig_WorkspacePath_WithOmnipusHome(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", "/custom/omnipus/home")

	cfg := DefaultConfig()
	want := filepath.Join("/custom/omnipus/home", "workspace")

	if cfg.Agents.Defaults.Home != want {
		t.Errorf("Workspace path with OMNIPUS_HOME = %q, want %q", cfg.Agents.Defaults.Home, want)
	}
}

// TestFlexibleStringSlice_UnmarshalText tests UnmarshalText with various comma separators
func TestFlexibleStringSlice_UnmarshalText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "English commas only",
			input:    "123,456,789",
			expected: []string{"123", "456", "789"},
		},
		{
			name:     "Chinese commas only",
			input:    "123，456，789",
			expected: []string{"123", "456", "789"},
		},
		{
			name:     "Mixed English and Chinese commas",
			input:    "123,456，789",
			expected: []string{"123", "456", "789"},
		},
		{
			name:     "Single value",
			input:    "123",
			expected: []string{"123"},
		},
		{
			name:     "Values with whitespace",
			input:    " 123 , 456 , 789 ",
			expected: []string{"123", "456", "789"},
		},
		{
			name:     "Empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "Only commas - English",
			input:    ",,",
			expected: []string{},
		},
		{
			name:     "Only commas - Chinese",
			input:    "，，",
			expected: []string{},
		},
		{
			name:     "Mixed commas with empty parts",
			input:    "123,,456，，789",
			expected: []string{"123", "456", "789"},
		},
		{
			name:     "Complex mixed values",
			input:    "user1@example.com，user2@test.com, admin@domain.org",
			expected: []string{"user1@example.com", "user2@test.com", "admin@domain.org"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f FlexibleStringSlice
			err := f.UnmarshalText([]byte(tt.input))
			if err != nil {
				t.Fatalf("UnmarshalText(%q) error = %v", tt.input, err)
			}

			if tt.expected == nil {
				if f != nil {
					t.Errorf("UnmarshalText(%q) = %v, want nil", tt.input, f)
				}
				return
			}

			if len(f) != len(tt.expected) {
				t.Errorf("UnmarshalText(%q) length = %d, want %d", tt.input, len(f), len(tt.expected))
				return
			}

			for i, v := range tt.expected {
				if f[i] != v {
					t.Errorf("UnmarshalText(%q)[%d] = %q, want %q", tt.input, i, f[i], v)
				}
			}
		})
	}
}

// TestFlexibleStringSlice_UnmarshalText_EmptySliceConsistency tests nil vs empty slice behavior
func TestFlexibleStringSlice_UnmarshalText_EmptySliceConsistency(t *testing.T) {
	t.Run("Empty string returns nil", func(t *testing.T) {
		var f FlexibleStringSlice
		err := f.UnmarshalText([]byte(""))
		if err != nil {
			t.Fatalf("UnmarshalText error = %v", err)
		}
		if f != nil {
			t.Errorf("Empty string should return nil, got %v", f)
		}
	})

	t.Run("Commas only returns empty slice", func(t *testing.T) {
		var f FlexibleStringSlice
		err := f.UnmarshalText([]byte(",,,"))
		if err != nil {
			t.Fatalf("UnmarshalText error = %v", err)
		}
		if f == nil {
			t.Error("Commas only should return empty slice, not nil")
		}
		if len(f) != 0 {
			t.Errorf("Expected empty slice, got %v", f)
		}
	})
}

func TestFlexibleStringSlice_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single string",
			input:    `"Thinking..."`,
			expected: []string{"Thinking..."},
		},
		{
			name:     "single number",
			input:    `123`,
			expected: []string{"123"},
		},
		{
			name:     "string array",
			input:    `["Thinking...", "Still working..."]`,
			expected: []string{"Thinking...", "Still working..."},
		},
		{
			name:     "mixed array",
			input:    `["123", 456]`,
			expected: []string{"123", "456"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f FlexibleStringSlice
			if err := json.Unmarshal([]byte(tt.input), &f); err != nil {
				t.Fatalf("json.Unmarshal(%s) error = %v", tt.input, err)
			}
			if len(f) != len(tt.expected) {
				t.Fatalf("json.Unmarshal(%s) len = %d, want %d", tt.input, len(f), len(tt.expected))
			}
			for i, want := range tt.expected {
				if f[i] != want {
					t.Fatalf("json.Unmarshal(%s)[%d] = %q, want %q", tt.input, i, f[i], want)
				}
			}
		})
	}
}

func TestLoadConfig_TelegramPlaceholderTextAcceptsSingleString(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	data := `{
		"version": 1,
		"agents": { "defaults": { "workspace": "", "model": "", "max_tokens": 0, "max_tool_iterations": 0 } },
		"bindings": [],
		"session": {},
		"channels": {
			"telegram": {
				"enabled": true,
				"bot_token": "",
				"allow_from": [],
				"placeholder": {
					"enabled": true,
					"text": "Thinking..."
				}
			}
		},
		"providers": [],
		"gateway": {},
		"tools": {},
		"heartbeat": {},
		"devices": {},
		"voice": {}
	}`
	if err := os.WriteFile(cfgPath, []byte(data), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := []string(cfg.Channels["telegram"].Placeholder.Text); len(got) != 1 || got[0] != "Thinking..." {
		t.Fatalf("placeholder.text = %#v, want [\"Thinking...\"]", got)
	}
}

func TestConfigParsesLogLevel(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	data := `{"version":1,"gateway":{"log_level":"debug"}}`
	if err := os.WriteFile(cfgPath, []byte(data), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Gateway.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want \"debug\"", cfg.Gateway.LogLevel)
	}
}

func TestConfigLogLevelEmpty(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	data := `{"version":1}`
	if err := os.WriteFile(cfgPath, []byte(data), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// When config omits log_level, the DefaultConfig value ("fatal") is preserved.
	if cfg.Gateway.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want \"fatal\"", cfg.Gateway.LogLevel)
	}
}

func TestModelConfig_ExtraBodyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfg := &Config{
		Version: CurrentVersion,
		Providers: []*ModelConfig{
			{
				ModelName: "test-model",
				Model:     "openai/test",
				APIKeyRef: "TEST_OPENAI_KEY",
				ExtraBody: map[string]any{"custom_field": "value", "num_field": 42},
			},
		},
	}

	if err := SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}

	loaded, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}

	if loaded.Providers[0].ExtraBody == nil {
		t.Fatal("ExtraBody should not be nil after round-trip")
	}
	if got := loaded.Providers[0].ExtraBody["custom_field"]; got != "value" {
		t.Errorf("ExtraBody[custom_field] = %v, want value", got)
	}
	if got := loaded.Providers[0].ExtraBody["num_field"]; got != float64(42) {
		t.Errorf("ExtraBody[num_field] = %v, want 42", got)
	}
}

func TestDefaultConfig_MinimaxExtraBody(t *testing.T) {
	cfg := DefaultConfig()

	var minimaxCfg *ModelConfig
	for i := range cfg.Providers {
		if cfg.Providers[i].Model == "minimax/MiniMax-M2.5" {
			minimaxCfg = cfg.Providers[i]
			break
		}
	}
	if minimaxCfg == nil {
		t.Fatal("Minimax model not found in Providers")
	}
	if minimaxCfg.ExtraBody == nil {
		t.Fatal("Minimax ExtraBody should not be nil")
	}
	if got, ok := minimaxCfg.ExtraBody["reasoning_split"]; !ok || got != true {
		t.Fatalf("Minimax ExtraBody[reasoning_split] = %v, want true", got)
	}
}

func TestFilterSensitiveData(t *testing.T) {
	// Test with nil security config
	cfg := &Config{}
	if got := cfg.FilterSensitiveData("hello sk-key123 world"); got != "hello sk-key123 world" {
		t.Errorf("nil security: got %q, want original", got)
	}

	// Test with empty content
	if got := cfg.FilterSensitiveData(""); got != "" {
		t.Errorf("empty content: got %q, want empty", got)
	}

	// Test short content (less than FilterMinLength=8, should skip filtering)
	cfg.Tools.FilterSensitiveData = true
	cfg.Tools.FilterMinLength = 8

	if got := cfg.FilterSensitiveData("sk-key"); got != "sk-key" {
		t.Errorf("short content should not be filtered: got %q", got)
	}

	// Test disabled filtering
	content := "some long content that would normally be filtered"
	cfg.Tools.FilterSensitiveData = false
	if got := cfg.FilterSensitiveData(content); got != content {
		t.Errorf("disabled filtering: got %q, want original %q", got, content)
	}
}

func TestFilterSensitiveData_MultipleKeys(t *testing.T) {
	// All credential fields (channel secrets, model API keys, and web tool keys) are now
	// stored as env var refs (not SecureString). FilterSensitiveData operates on SecureString
	// values that are still in-memory (e.g. from providers with legacy api_key fields).
	// This test validates that filtering still works when SecureString values are present
	// (e.g. during migration or for providers that have not yet been migrated to Ref-based keys).
	cfg := &Config{
		Tools: ToolsConfig{
			FilterSensitiveData: true,
			FilterMinLength:     8,
		},
	}

	// With no SecureString values, filtering is a no-op
	content := "nothing to filter here"
	if got := cfg.FilterSensitiveData(content); got != content {
		t.Errorf("no-op filtering: got %q, want %q", got, content)
	}
}

func TestFilterSensitiveData_AllTokenTypes(t *testing.T) {
	// All credential fields (channel secrets, model API keys, web tool keys) are now
	// stored as env var refs (APIKeyRef) and injected via os.Setenv at boot.
	// collectSensitive uses reflection to find SecureString values — after the migration
	// to Ref-based fields, no web tool keys are stored as SecureString.
	// This test validates the filtering path is functional when there are no SecureString values.
	cfg := &Config{
		Tools: ToolsConfig{
			FilterSensitiveData: true,
			FilterMinLength:     8,
			Web: WebToolsConfig{
				Brave:       BraveConfig{APIKeyRef: "BRAVE_API_KEY"},
				Tavily:      TavilyConfig{APIKeyRef: "TAVILY_API_KEY"},
				Perplexity:  PerplexityConfig{APIKeyRef: "PERPLEXITY_API_KEY"},
				GLMSearch:   GLMSearchConfig{APIKeyRef: "GLM_API_KEY"},
				BaiduSearch: BaiduSearchConfig{APIKeyRef: "BAIDU_API_KEY"},
			},
		},
	}

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "ref_name_is_not_filtered",
			content: "BRAVE_API_KEY is just a ref name, not a secret",
			want:    "BRAVE_API_KEY is just a ref name, not a secret",
		},
		{
			name:    "short_key_not_filtered",
			content: "Key abc not filtered because length < 8",
			want:    "Key abc not filtered because length < 8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cfg.FilterSensitiveData(tt.content); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// --- ResolveType tests ---

// TestResolveType_ExplicitType verifies that when AgentConfig.Type is set, it
// is returned directly without inspecting the ID or calling isCoreAgent.
//
// BDD: Given an AgentConfig with Type="core"
//
//	When ResolveType is called
//	Then "core" is returned without calling isCoreAgent
//
// Traces to: config.go AgentConfig.ResolveType — "if a.Type != "" { return a.Type }" branch
func TestResolveType_ExplicitType(t *testing.T) {
	a := AgentConfig{ID: "some-agent", Type: AgentTypeCore}

	// isCoreAgent must not be called when Type is set explicitly.
	called := false
	got := a.ResolveType(func(id string) bool {
		called = true
		return false
	})

	if got != AgentTypeCore {
		t.Errorf("ResolveType() = %q, want %q", got, AgentTypeCore)
	}
	if called {
		t.Error("isCoreAgent should not be called when Type is set explicitly")
	}

	// Differentiation: a different explicit type returns a different result.
	a2 := AgentConfig{ID: "some-agent", Type: AgentTypeSystem}
	got2 := a2.ResolveType(nil)
	if got2 != AgentTypeSystem {
		t.Errorf("ResolveType() = %q, want %q", got2, AgentTypeSystem)
	}
	if got == got2 {
		t.Error("different explicit types must produce different results")
	}
}

// TestResolveType_ExplicitSystemType verifies that an AgentConfig with
// Type=AgentTypeSystem set explicitly resolves to AgentTypeSystem.
//
// BDD: Given an AgentConfig with Type=AgentTypeSystem
//
//	When ResolveType is called
//	Then AgentTypeSystem is returned
//
// Traces to: config.go AgentConfig.ResolveType — explicit type takes priority (FR-045)
func TestResolveType_ExplicitSystemType(t *testing.T) {
	a := AgentConfig{ID: "any-id", Type: AgentTypeSystem}

	got := a.ResolveType(func(_ string) bool { return false })

	if got != AgentTypeSystem {
		t.Errorf("ResolveType() = %q, want AgentTypeSystem", got)
	}
}

// TestResolveType_CoreAgentID verifies that an agent whose ID is recognized by
// isCoreAgent resolves to AgentTypeCore.
//
// BDD: Given an AgentConfig with ID="researcher" and isCoreAgent returns true for it
//
//	When ResolveType is called
//	Then AgentTypeCore is returned
//
// Traces to: config.go AgentConfig.ResolveType — isCoreAgent(a.ID) branch
func TestResolveType_CoreAgentID(t *testing.T) {
	a := AgentConfig{ID: "researcher"}

	coreIDs := map[string]bool{"researcher": true, "assistant": true}
	got := a.ResolveType(func(id string) bool { return coreIDs[id] })

	if got != AgentTypeCore {
		t.Errorf("ResolveType() = %q, want AgentTypeCore", got)
	}

	// Differentiation: a different ID not in coreIDs resolves to custom.
	a2 := AgentConfig{ID: "my-custom-bot"}
	got2 := a2.ResolveType(func(id string) bool { return coreIDs[id] })
	if got2 != AgentTypeCustom {
		t.Errorf("ResolveType() = %q, want AgentTypeCustom for unrecognized ID", got2)
	}
	if got == got2 {
		t.Error("core agent ID and custom agent ID must resolve to different types")
	}
}

// TestResolveType_CustomAgentID verifies that an agent not recognized by
// isCoreAgent and not the system ID resolves to AgentTypeCustom.
//
// BDD: Given an AgentConfig with ID="my-bot" and isCoreAgent returns false
//
//	When ResolveType is called
//	Then AgentTypeCustom is returned
//
// Traces to: config.go AgentConfig.ResolveType — else → AgentTypeCustom
func TestResolveType_CustomAgentID(t *testing.T) {
	a := AgentConfig{ID: "my-bot"}

	got := a.ResolveType(func(_ string) bool { return false })

	if got != AgentTypeCustom {
		t.Errorf("ResolveType() = %q, want AgentTypeCustom", got)
	}
}

// TestResolveType_NilCallback verifies that passing nil for isCoreAgent does
// not panic; the agent falls through to AgentTypeCustom.
//
// BDD: Given an AgentConfig with ID="my-bot" and isCoreAgent=nil
//
//	When ResolveType is called
//	Then AgentTypeCustom is returned without panicking
//
// Traces to: config.go AgentConfig.ResolveType — nil isCoreAgent guard
func TestResolveType_NilCallback(t *testing.T) {
	a := AgentConfig{ID: "my-bot"}

	// Must not panic.
	got := a.ResolveType(nil)

	if got != AgentTypeCustom {
		t.Errorf("ResolveType() = %q, want AgentTypeCustom when isCoreAgent is nil", got)
	}
}

// --- AgentToolsCfg JSON round-trip test ---

// TestAgentToolsCfg_JSONRoundTrip verifies that AgentToolsCfg serializes to
// JSON and deserializes back with all fields preserved, including nested structs
// (Builtin.Mode, Builtin.Visible, MCP.Servers).
//
// BDD: Given a fully-populated AgentToolsCfg
//
//	When it is marshaled to JSON and unmarshaled back
//	Then all field values are identical to the original
//
// Traces to: config.go AgentToolsCfg, AgentBuiltinToolsCfg, AgentMCPToolsCfg
func TestAgentToolsCfg_JSONRoundTrip(t *testing.T) {
	original := AgentToolsCfg{
		Builtin: AgentBuiltinToolsCfg{
			Policies: map[string]ToolPolicy{
				"exec":       ToolPolicyAllow,
				"search_web": ToolPolicyAllow,
				"read_file":  ToolPolicyAllow,
			},
		},
		MCP: AgentMCPToolsCfg{
			Servers: []AgentMCPServerBinding{
				{ID: "github-server", Tools: []string{"create_issue", "list_prs"}},
				{ID: "jira-server", Tools: []string{"*"}},
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal(AgentToolsCfg): %v", err)
	}
	if len(data) == 0 {
		t.Fatal("marshaled JSON must not be empty")
	}

	var decoded AgentToolsCfg
	if unmarshalErr := json.Unmarshal(data, &decoded); unmarshalErr != nil {
		t.Fatalf("json.Unmarshal(AgentToolsCfg): %v", unmarshalErr)
	}

	// Builtin.Policies — count
	if len(decoded.Builtin.Policies) != len(original.Builtin.Policies) {
		t.Fatalf("Builtin.Policies len = %d, want %d", len(decoded.Builtin.Policies), len(original.Builtin.Policies))
	}

	// MCP.Servers — count and contents
	if len(decoded.MCP.Servers) != len(original.MCP.Servers) {
		t.Fatalf("MCP.Servers len = %d, want %d", len(decoded.MCP.Servers), len(original.MCP.Servers))
	}
	for i, srv := range original.MCP.Servers {
		if decoded.MCP.Servers[i].ID != srv.ID {
			t.Errorf("MCP.Servers[%d].ID = %q, want %q", i, decoded.MCP.Servers[i].ID, srv.ID)
		}
		if len(decoded.MCP.Servers[i].Tools) != len(srv.Tools) {
			t.Errorf("MCP.Servers[%d].Tools len = %d, want %d",
				i, len(decoded.MCP.Servers[i].Tools), len(srv.Tools))
		}
	}

	// Content differentiation: marshal again and compare bytes to confirm
	// the round-trip is stable (not just shape-preserving).
	data2, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("second json.Marshal: %v", err)
	}
	if string(data) != string(data2) {
		t.Errorf("second marshal differs from first:\n  first:  %s\n  second: %s", data, data2)
	}
}

func TestRetention_ZeroSessionDaysStillMeansDefault90(t *testing.T) {
	r := OmnipusRetentionConfig{SessionDays: 0}
	if got := r.RetentionSessionDays(); got != 90 {
		t.Errorf("RetentionSessionDays() = %d; want 90 for zero SessionDays", got)
	}
}

func TestRetention_DisabledFlagMeansKeepForever(t *testing.T) {
	enabled := OmnipusRetentionConfig{Disabled: false}
	if enabled.IsDisabled() {
		t.Error("IsDisabled() = true; want false when Disabled field is false")
	}

	disabled := OmnipusRetentionConfig{Disabled: true}
	if !disabled.IsDisabled() {
		t.Error("IsDisabled() = false; want true when Disabled field is true")
	}
}

func TestOmnipusRetentionConfig_Mode_Default(t *testing.T) {
	r := OmnipusRetentionConfig{SessionDays: 0, Disabled: false}
	if got := r.Mode(); got != RetentionDefault {
		t.Errorf("Mode() = %v; want RetentionDefault for {SessionDays:0, Disabled:false}", got)
	}
	if got := r.Mode().String(); got != "default" {
		t.Errorf("Mode().String() = %q; want \"default\"", got)
	}
}

func TestOmnipusRetentionConfig_Mode_Custom(t *testing.T) {
	r := OmnipusRetentionConfig{SessionDays: 30, Disabled: false}
	if got := r.Mode(); got != RetentionCustom {
		t.Errorf("Mode() = %v; want RetentionCustom for {SessionDays:30, Disabled:false}", got)
	}
	if got := r.Mode().String(); got != "custom" {
		t.Errorf("Mode().String() = %q; want \"custom\"", got)
	}
}

func TestOmnipusRetentionConfig_Mode_Forever(t *testing.T) {
	r := OmnipusRetentionConfig{SessionDays: 0, Disabled: true}
	if got := r.Mode(); got != RetentionForever {
		t.Errorf("Mode() = %v; want RetentionForever for {SessionDays:0, Disabled:true}", got)
	}
	if got := r.Mode().String(); got != "forever" {
		t.Errorf("Mode().String() = %q; want \"forever\"", got)
	}
}

func TestOmnipusRetentionConfig_Mode_DisabledTakesPrecedence(t *testing.T) {
	// Disabled:true with a non-zero SessionDays must still resolve to RetentionForever.
	r := OmnipusRetentionConfig{SessionDays: 99, Disabled: true}
	if got := r.Mode(); got != RetentionForever {
		t.Errorf("Mode() = %v; want RetentionForever when Disabled=true overrides SessionDays=99", got)
	}
	if got := r.Mode().String(); got != "forever" {
		t.Errorf("Mode().String() = %q; want \"forever\"", got)
	}
}

// TestAgentConfig_ShellPolicy_RoundTrip verifies that AgentConfig with a
// shell_policy field marshals and unmarshals without data loss. This is a
// change-guard: if the field is accidentally removed or renamed, this test
// will fail.
func TestAgentConfig_ShellPolicy_RoundTrip(t *testing.T) {
	trueBool := true
	original := AgentConfig{
		ID:   "test-agent",
		Name: "Test Agent",
		ShellPolicy: &AgentShellPolicy{
			EnableDenyPatterns: trueBool,
			CustomDenyPatterns: []string{`^\s*rm\s+-rf`, `curl.*\|.*sh`},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal(AgentConfig) error: %v", err)
	}

	var decoded AgentConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(AgentConfig) error: %v", err)
	}

	if decoded.ShellPolicy == nil {
		t.Fatal("ShellPolicy: got nil, want non-nil")
	}
	if decoded.ShellPolicy.EnableDenyPatterns != original.ShellPolicy.EnableDenyPatterns {
		t.Errorf("ShellPolicy.EnableDenyPatterns: got %v, want %v",
			decoded.ShellPolicy.EnableDenyPatterns, original.ShellPolicy.EnableDenyPatterns)
	}
	if len(decoded.ShellPolicy.CustomDenyPatterns) != len(original.ShellPolicy.CustomDenyPatterns) {
		t.Fatalf("ShellPolicy.CustomDenyPatterns: len %d, want %d",
			len(decoded.ShellPolicy.CustomDenyPatterns), len(original.ShellPolicy.CustomDenyPatterns))
	}
	for i, p := range original.ShellPolicy.CustomDenyPatterns {
		if decoded.ShellPolicy.CustomDenyPatterns[i] != p {
			t.Errorf("CustomDenyPatterns[%d]: got %q, want %q",
				i, decoded.ShellPolicy.CustomDenyPatterns[i], p)
		}
	}
}

// TestAgentConfig_ShellPolicy_OmittedWhenEmpty confirms that omitempty
// suppresses shell_policy when it holds its zero value, so existing configs
// without this field are unaffected.
func TestAgentConfig_ShellPolicy_OmittedWhenEmpty(t *testing.T) {
	cfg := AgentConfig{ID: "minimal"}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	s := string(data)
	if strings.Contains(s, "shell_policy") {
		t.Errorf("shell_policy must be omitted when nil; got: %s", s)
	}
}

// TestLoadConfig_LegacySandboxProfileFields_Ignored is a pinning test for
// Fix 4 of the 7-reviewer SandboxProfile-removal gate (silent-failure-hunter
// + pr-test-analyzer): ADR-035 explicitly chose "no backward compatibility"
// for the retired per-agent sandbox_profile and global sandbox.default_profile
// fields, rather than a migration. LoadConfig has no DisallowUnknownFields
// anywhere on the config-load path, so a persisted config.json from before
// this change — carrying a per-agent "sandbox_profile":"off" and a global
// "sandbox":{"default_profile":"host"} — must still load without error today
// (unknown keys silently ignored), with every other field on both the agent
// and the sandbox section intact. This pins that behavior so a future change
// to the config-load path (e.g. adding strict decoding somewhere, which this
// codebase does elsewhere — see decodeAgentCreateVariant's
// DisallowUnknownFields) doesn't silently break upgrades by turning a
// harmless legacy key into a boot-time hard failure.
func TestLoadConfig_LegacySandboxProfileFields_Ignored(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	rawCfg := `{
		"version": 1,
		"agents": {
			"defaults": {"workspace": "` + tmpDir + `", "model_name": "test-model", "max_tokens": 4096},
			"list": [
				{
					"id": "legacy-agent",
					"name": "Legacy Agent",
					"description": "carries a retired per-agent sandbox_profile key",
					"sandbox_profile": "off"
				}
			]
		},
		"sandbox": {
			"mode": "enforce",
			"audit_log": true,
			"default_profile": "host"
		}
	}`
	if err := os.WriteFile(cfgPath, []byte(rawCfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig must not error on legacy sandbox_profile fields, got: %v", err)
	}

	// ADR-054 (entity/config separation) landed after this test was
	// originally written: agents.list is no longer an entity inside
	// config.json — stripLegacyAgentsList unconditionally drops any legacy
	// agents.list content on load (loudly, via a WARN log), regardless of
	// whether an individual agent also carries a retired key like
	// sandbox_profile. So "legacy-agent" above no longer proves "an unknown
	// per-agent key doesn't corrupt sibling fields" (agents.list is emptied
	// either way now) — it still usefully proves LoadConfig doesn't error out
	// on the retired key while parsing the now-to-be-dropped legacy roster.
	// This pinning test's real remaining assertion is the sandbox section
	// below.
	if len(cfg.Agents.List) != 0 {
		t.Fatalf("expected agents.list to be stripped by ADR-054's stripLegacyAgentsList, got %d agent(s): %+v",
			len(cfg.Agents.List), cfg.Agents.List)
	}

	// The global sandbox section's retired default_profile key must not
	// prevent its real sibling fields from loading.
	if cfg.Sandbox.Mode != SandboxModeEnforce {
		t.Errorf("cfg.Sandbox.Mode = %q, want %q", cfg.Sandbox.Mode, SandboxModeEnforce)
	}
	if !cfg.Sandbox.AuditLog {
		t.Error("cfg.Sandbox.AuditLog should be true — must survive alongside the retired default_profile key")
	}
}

// TestLoadConfig_LegacyPreviewFields_Ignored is a pinning test for ADR-044
// (preview-on-main-listener): the separate preview listener and its
// gateway.preview_port / preview_host / preview_origin / preview_listener_enabled
// keys were deleted entirely with no back-compat, mirroring the ADR-035
// SandboxProfile-removal precedent (TestLoadConfig_LegacySandboxProfileFields_Ignored
// above). LoadConfig has no DisallowUnknownFields on the config-load path, so a
// persisted config.json from before this change — carrying the four retired
// preview keys — must still load without error today (unknown keys silently
// ignored), with gateway.public_url and every other real field intact, and the
// new gateway.preview_enabled resolving to its semantic default (true) since
// the legacy config.json never set it.
func TestLoadConfig_LegacyPreviewFields_Ignored(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	rawCfg := `{
		"version": 1,
		"agents": {
			"defaults": {"workspace": "` + tmpDir + `", "model_name": "test-model", "max_tokens": 4096}
		},
		"gateway": {
			"host": "127.0.0.1",
			"port": 5000,
			"public_url": "https://pod.example.com",
			"preview_port": 5001,
			"preview_host": "127.0.0.1",
			"preview_origin": "https://preview.example.com",
			"preview_listener_enabled": false
		}
	}`
	if err := os.WriteFile(cfgPath, []byte(rawCfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig must not error on legacy preview fields, got: %v", err)
	}

	// Real sibling fields in the same gateway object must survive alongside the
	// retired preview keys.
	if cfg.Gateway.Host != "127.0.0.1" {
		t.Errorf("cfg.Gateway.Host = %q, want %q", cfg.Gateway.Host, "127.0.0.1")
	}
	if cfg.Gateway.Port != 5000 {
		t.Errorf("cfg.Gateway.Port = %d, want 5000", cfg.Gateway.Port)
	}
	if cfg.Gateway.PublicURL != "https://pod.example.com" {
		t.Errorf("cfg.Gateway.PublicURL = %q, want %q — must survive alongside the retired preview keys",
			cfg.Gateway.PublicURL, "https://pod.example.com")
	}

	// The retired preview_listener_enabled=false in the legacy JSON must NOT
	// suppress the new preview_enabled default: the legacy key and the new key
	// are unrelated on the wire (different JSON names), so a config that never
	// set preview_enabled resolves to the semantic default (true).
	if !cfg.IsPreviewEnabled() {
		t.Error("cfg.IsPreviewEnabled() should be true — the legacy preview_listener_enabled key " +
			"must not be silently reinterpreted as the new preview_enabled")
	}
}

// TestLoadConfig_LegacyTurnSyntheticErrorFloor_Ignored is a pinning test for
// ADR-058 (tool-denial semantics): FR-084's synthetic-error turn-abort floor
// and its gateway.turn_synthetic_error_floor config key are deleted in full,
// with no migration (docs/internal/specs/adr-058-tool-denial-semantics-spec.md
// FR-058-14 — "FR-084 is deleted in full: config.GatewayConfig.
// TurnSyntheticErrorFloor, ..., and every comment referencing them";
// docs/internal/specs/tool-registry-redesign-spec.md's FR-084 entry is
// retained but marked superseded). LoadConfig has no DisallowUnknownFields
// anywhere on the config-load path, so a persisted config.json from before
// this change — carrying a "turn_synthetic_error_floor" key under "gateway"
// — must still load without error today (unknown keys silently ignored),
// with every other field in the same gateway object intact.
//
// This mirrors TestLoadConfig_LegacySandboxProfileFields_Ignored (ADR-035)
// and TestLoadConfig_LegacyPreviewFields_Ignored (ADR-044) directly above:
// both were written explicitly as pins against "a future change to the
// config-load path (e.g. adding strict decoding somewhere, which this
// codebase does elsewhere)" silently turning a harmless legacy key into a
// boot-time hard failure. This closes the identical gap for ADR-058's own
// retired key, discharging spec §10's DoD item verbatim: "One boot with a
// legacy `turn_synthetic_error_floor` key present in config.json starts
// cleanly" — previously verified only by hand with a throwaway program
// during implementation, with no regression guard left behind.
//
// The two gateway sibling-field assertions below are the positive lower
// bound (Binding Rule 4): without them, this test would pass equally well
// for a broken implementation that silently discarded the whole gateway
// section — or returned an empty *Config — whenever it hit an unrecognised
// key inside it. Asserting Host/Port survive with their ACTUAL configured
// values (not just "LoadConfig returned no error") is what rules that stub
// out.
func TestLoadConfig_LegacyTurnSyntheticErrorFloor_Ignored(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	rawCfg := `{
		"version": 1,
		"agents": {
			"defaults": {"workspace": "` + tmpDir + `", "model_name": "test-model", "max_tokens": 4096}
		},
		"gateway": {
			"host": "127.0.0.1",
			"port": 5000,
			"turn_synthetic_error_floor": 8
		}
	}`
	if err := os.WriteFile(cfgPath, []byte(rawCfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig must not error on the legacy turn_synthetic_error_floor key, got: %v", err)
	}

	if cfg.Gateway.Host != "127.0.0.1" {
		t.Errorf("cfg.Gateway.Host = %q, want %q — must survive alongside the retired "+
			"turn_synthetic_error_floor key", cfg.Gateway.Host, "127.0.0.1")
	}
	if cfg.Gateway.Port != 5000 {
		t.Errorf("cfg.Gateway.Port = %d, want 5000 — must survive alongside the retired "+
			"turn_synthetic_error_floor key", cfg.Gateway.Port)
	}
}

// TestConfig_IsPreviewEnabled verifies the semantics of
// gateway.preview_enabled (ADR-044, FR-006, TDA-1): a nil *Config RECEIVER
// fails closed (false — no config to consult); a non-nil *Config with the
// field unset resolves to the field-level default (true); the field resolves
// to its explicit value otherwise. The method lives on *Config (not
// *GatewayConfig) per the shared cross-agent contract for this feature.
func TestConfig_IsPreviewEnabled(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name string
		cfg  *Config
		want bool
	}{
		{name: "nil receiver fails closed (false) — no config to consult", cfg: nil, want: false},
		{name: "non-nil config, field unset, defaults to true", cfg: &Config{}, want: true},
		{name: "explicit true", cfg: &Config{Gateway: GatewayConfig{PreviewEnabled: &trueVal}}, want: true},
		{name: "explicit false", cfg: &Config{Gateway: GatewayConfig{PreviewEnabled: &falseVal}}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.IsPreviewEnabled()
			if got != tc.want {
				t.Errorf("IsPreviewEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestConfig_IsPreviewEnabled_NilPointerExplicit is a standalone, unmistakably
// explicit regression pin for TDA-1: a literal `var nilCfg *Config` (as
// opposed to a table-driven `cfg: nil` field, which some readers might assume
// gets boxed into a non-nil zero value) must resolve IsPreviewEnabled() to
// false. Calling a method on a nil pointer receiver is valid Go as long as
// the method doesn't dereference before its own nil check, which is exactly
// what IsPreviewEnabled's `if c == nil` guard exists to allow.
func TestConfig_IsPreviewEnabled_NilPointerExplicit(t *testing.T) {
	var nilCfg *Config
	if got := nilCfg.IsPreviewEnabled(); got != false {
		t.Errorf("nilCfg.IsPreviewEnabled() = %v, want false (fail-closed on nil receiver)", got)
	}

	// Differentiation: a real, non-nil *Config{} with the field unset must
	// resolve to the OPPOSITE value (true) — proving the nil-receiver check
	// and the field-level ResolveBool default are two genuinely different
	// code paths, not one masking the other.
	if got := (&Config{}).IsPreviewEnabled(); got != true {
		t.Errorf("(&Config{}).IsPreviewEnabled() = %v, want true (field-level default)", got)
	}
}

// writeMinimalLoadableConfig writes a config.json that LoadConfig can load
// without error (valid version/agents-defaults/gateway block), with
// gateway.public_url set to publicURL (empty string omits the key entirely,
// matching an operator who never configured it).
func writeMinimalLoadableConfig(t *testing.T, publicURL string) string {
	t.Helper()
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	publicURLLine := ""
	if publicURL != "" {
		publicURLLine = `,
			"public_url": "` + publicURL + `"`
	}

	rawCfg := `{
		"version": 1,
		"agents": {
			"defaults": {"workspace": "` + tmpDir + `", "model_name": "test-model", "max_tokens": 4096}
		},
		"gateway": {
			"host": "127.0.0.1",
			"port": 5000` + publicURLLine + `
		}
	}`
	if err := os.WriteFile(cfgPath, []byte(rawCfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

// TestLoadConfig_PublicURL_AutoDetectFromDevpodPreviewURL covers the W0
// boot-time auto-detection: LoadConfig fills an unset gateway.public_url
// from $DEVPOD_PREVIEW_URL so canonicalGatewayOrigin (and therefore
// serve_web/preview links and the agent's own reachable-URL preamble, W5)
// resolve to the pod's externally-reachable origin instead of the
// unreachable http://localhost:5000 default. An operator-set value must
// never be overridden, and when neither source is present the field must
// stay empty (existing wildcard-bind/derive-from-host:port behavior is
// untouched).
func TestLoadConfig_PublicURL_AutoDetectFromDevpodPreviewURL(t *testing.T) {
	t.Run("empty public_url + DEVPOD_PREVIEW_URL set resolves to env value", func(t *testing.T) {
		t.Setenv("DEVPOD_PREVIEW_URL", "https://pod-omnipus.fly.dev")
		cfgPath := writeMinimalLoadableConfig(t, "")

		cfg, err := LoadConfig(cfgPath)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.Gateway.PublicURL != "https://pod-omnipus.fly.dev" {
			t.Errorf("cfg.Gateway.PublicURL = %q, want auto-detected %q",
				cfg.Gateway.PublicURL, "https://pod-omnipus.fly.dev")
		}
	})

	t.Run("operator-set public_url wins over DEVPOD_PREVIEW_URL", func(t *testing.T) {
		t.Setenv("DEVPOD_PREVIEW_URL", "https://pod-omnipus.fly.dev")
		cfgPath := writeMinimalLoadableConfig(t, "https://operator.example.com")

		cfg, err := LoadConfig(cfgPath)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.Gateway.PublicURL != "https://operator.example.com" {
			t.Errorf("cfg.Gateway.PublicURL = %q, want operator value %q (must never be overridden by env auto-detect)",
				cfg.Gateway.PublicURL, "https://operator.example.com")
		}
	})

	t.Run("both empty stays empty", func(t *testing.T) {
		t.Setenv("DEVPOD_PREVIEW_URL", "")
		cfgPath := writeMinimalLoadableConfig(t, "")

		cfg, err := LoadConfig(cfgPath)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.Gateway.PublicURL != "" {
			t.Errorf("cfg.Gateway.PublicURL = %q, want empty (no operator value, no env value)",
				cfg.Gateway.PublicURL)
		}
	})

	// Regression (silent-failure audit): a bare pod's FIRST boot has no
	// config.json at all, so LoadConfig takes the os.IsNotExist ->
	// DefaultConfig() early-return path. The auto-detect previously ran only
	// AFTER that early return, so a fresh pod got public_url="" — defeating the
	// feature's own primary use case. seedPublicURLFromEnv now runs on every
	// return path, including this one.
	t.Run("fresh install (no config.json) still auto-detects — the primary use case", func(t *testing.T) {
		t.Setenv("DEVPOD_PREVIEW_URL", "https://pod-omnipus.fly.dev")
		missingPath := filepath.Join(t.TempDir(), "does-not-exist.json")

		cfg, err := LoadConfig(missingPath)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.Gateway.PublicURL != "https://pod-omnipus.fly.dev" {
			t.Errorf("fresh-install cfg.Gateway.PublicURL = %q, want auto-detected %q",
				cfg.Gateway.PublicURL, "https://pod-omnipus.fly.dev")
		}
	})

	// A trailing slash would make serve_web emit https://host//preview/... —
	// seedPublicURLFromEnv trims it.
	t.Run("trailing slash on DEVPOD_PREVIEW_URL is trimmed", func(t *testing.T) {
		t.Setenv("DEVPOD_PREVIEW_URL", "https://pod-omnipus.fly.dev/")
		cfgPath := writeMinimalLoadableConfig(t, "")

		cfg, err := LoadConfig(cfgPath)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.Gateway.PublicURL != "https://pod-omnipus.fly.dev" {
			t.Errorf("cfg.Gateway.PublicURL = %q, want trailing slash trimmed %q",
				cfg.Gateway.PublicURL, "https://pod-omnipus.fly.dev")
		}
	})
}

func TestLoadConfig_MissingVersionField(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"gateway":{"host":"127.0.0.1","port":5000}}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("LoadConfig must refuse a config with no version field")
	}
	if !strings.Contains(err.Error(), "missing a version field") {
		t.Fatalf("error = %q, want it to name the missing version field", err.Error())
	}
}
