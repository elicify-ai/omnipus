package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
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

func TestProvidersConfig_IsEmpty(t *testing.T) {
	var empty providersConfigV0
	t.Logf("empty: %+v", empty)
	if !empty.IsEmpty() {
		t.Fatal("empty providersConfig should report empty")
	}

	novita := providersConfigV0{
		Novita: providerConfigV0{
			APIKey: "test-key",
		},
	}
	if novita.IsEmpty() {
		t.Fatal("providersConfig with novita settings should not report empty")
	}
}

func TestAgentConfig_FullParse(t *testing.T) {
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

	if len(cfg.Agents.List) != 2 {
		t.Fatalf("agents.list len = %d, want 2", len(cfg.Agents.List))
	}

	sales := cfg.Agents.List[0]
	if sales.ID != "sales" || !sales.Default || sales.Name != "Sales Bot" {
		t.Errorf("sales = %+v", sales)
	}
	if sales.Model == nil || sales.Model.Primary != "gpt-4" {
		t.Errorf("sales.Model = %+v", sales.Model)
	}

	support := cfg.Agents.List[1]
	if support.ID != "support" || support.Name != "Support Bot" {
		t.Errorf("support = %+v", support)
	}
	if support.Model == nil || support.Model.Primary != "claude-opus" {
		t.Errorf("support.Model = %+v", support.Model)
	}
	if len(support.Model.Fallbacks) != 1 || support.Model.Fallbacks[0] != "haiku" {
		t.Errorf("support.Model.Fallbacks = %v", support.Model.Fallbacks)
	}
	if support.Subagents == nil || len(support.Subagents.AllowAgents) != 1 {
		t.Errorf("support.Subagents = %+v", support.Subagents)
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

// TestDefaultConfig_HeartbeatEnabled verifies heartbeat is enabled by default
func TestDefaultConfig_HeartbeatEnabled(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.Heartbeat.Enabled {
		t.Error("Heartbeat should be enabled by default")
	}
}

// TestDefaultConfig_WorkspacePath verifies workspace path is correctly set
func TestDefaultConfig_WorkspacePath(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.Workspace == "" {
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

// TestDefaultConfig_Gateway verifies gateway defaults
func TestDefaultConfig_Gateway(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Gateway.Host != "127.0.0.1" {
		t.Error("Gateway host should have default value")
	}
	if cfg.Gateway.Port == 0 {
		t.Error("Gateway port should have default value")
	}
	if cfg.Gateway.HotReload {
		t.Error("Gateway hot reload should be disabled by default")
	}
}

// TestDefaultConfig_Channels verifies channels are disabled by default
func TestDefaultConfig_Channels(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Channels.Telegram.Enabled {
		t.Error("Telegram should be disabled by default")
	}
	if cfg.Channels.Discord.Enabled {
		t.Error("Discord should be disabled by default")
	}
	if cfg.Channels.Slack.Enabled {
		t.Error("Slack should be disabled by default")
	}
	if cfg.Channels.Matrix.Enabled {
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
	cfg.Channels.Telegram.Placeholder.Enabled = false

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
	if loaded.Channels.Telegram.Placeholder.Enabled {
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

	if cfg.Agents.Defaults.Workspace == "" {
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
	if !cfg.Heartbeat.Enabled {
		t.Error("Heartbeat should be enabled by default")
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
	if err := os.WriteFile(configPath, []byte(`{"tools":{"web":{"prefer_native":false}}}`), 0o600); err != nil {
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
// TestDefaultConfig_SummarizationThresholds verifies summarization defaults
func TestDefaultConfig_SummarizationThresholds(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.SummarizeMessageThreshold != 20 {
		t.Errorf("SummarizeMessageThreshold = %d, want 20", cfg.Agents.Defaults.SummarizeMessageThreshold)
	}
	if cfg.Agents.Defaults.SummarizeTokenPercent != 75 {
		t.Errorf("SummarizeTokenPercent = %d, want 75", cfg.Agents.Defaults.SummarizeTokenPercent)
	}
}

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

	if cfg.Agents.Defaults.Workspace != want {
		t.Errorf("Default workspace path = %q, want %q", cfg.Agents.Defaults.Workspace, want)
	}
}

func TestDefaultConfig_WorkspacePath_WithOmnipusHome(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", "/custom/omnipus/home")

	cfg := DefaultConfig()
	want := filepath.Join("/custom/omnipus/home", "workspace")

	if cfg.Agents.Defaults.Workspace != want {
		t.Errorf("Workspace path with OMNIPUS_HOME = %q, want %q", cfg.Agents.Defaults.Workspace, want)
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
	if got := []string(cfg.Channels.Telegram.Placeholder.Text); len(got) != 1 || got[0] != "Thinking..." {
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

// TestAgentConfig_IsActive_NilDefaultsToTrue verifies the backward-compat
// rule: an AgentConfig with no Enabled field (nil pointer) is treated as
// active so existing configs without the field continue to work.
//
// Traces to: wave5b-system-agent-spec.md — BRD §D.4.2 enabled semantics
func TestAgentConfig_IsActive_NilDefaultsToTrue(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name    string
		enabled *bool
		want    bool
	}{
		{"nil pointer is active", nil, true},
		{"explicit true is active", &trueVal, true},
		{"explicit false is inactive", &falseVal, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := AgentConfig{ID: "test", Enabled: tt.enabled}
			if got := a.IsActive(); got != tt.want {
				t.Errorf("IsActive() = %v, want %v", got, tt.want)
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
			DefaultPolicy: ToolPolicyDeny,
			Policies: map[string]ToolPolicy{
				"exec":       ToolPolicyAllow,
				"web_search": ToolPolicyAllow,
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

	// Builtin.DefaultPolicy
	if decoded.Builtin.DefaultPolicy != original.Builtin.DefaultPolicy {
		t.Errorf("Builtin.DefaultPolicy = %q, want %q", decoded.Builtin.DefaultPolicy, original.Builtin.DefaultPolicy)
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

// TestDeprecatedEnableFlagsAreIgnored documents the post-refactor behavior:
// tools.<name>.enabled is no longer consulted for gating. Tools always register
// and the policy engine (pkg/policy) controls invocation via allow/ask/deny.
// The legacy field remains in ToolsConfig so old configs still parse, but
// warnDeprecatedEnableFlags emits a one-time WARN on load when any sub-flag
// is explicitly false.
//
// BDD: Given a ToolsConfig with Browser.Enabled = false (legacy),
//
//	When warnDeprecatedEnableFlags is called,
//	Then no panic occurs and the flag has no runtime effect.
//
// Traces to: the tool-enable refactor that removed IsToolEnabled.
func TestDeprecatedEnableFlagsAreIgnored(t *testing.T) {
	cfg := &ToolsConfig{}
	cfg.Browser.Enabled = false
	cfg.Browser.EvaluateEnabled = false
	// Must not panic. We can't easily assert on the WARN log without a
	// test logger, but the call exercises the entire scan path.
	cfg.warnDeprecatedEnableFlags()

	// Flag stays on the struct (back-compat for serialization).
	if cfg.Browser.Enabled {
		t.Error("Browser.Enabled should remain whatever the caller set (false here)")
	}
	// There is no behavioral contract to test here — that's the whole point.
	// The matching behavioral contract lives in pkg/policy tests (browser.evaluate
	// is denied by default via builtinToolPolicies).
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

// TestAgentConfig_SandboxProfile_RoundTrip verifies that AgentConfig with
// sandbox_profile and shell_policy fields marshals and unmarshals without data
// loss. This is a change-guard: if the fields are accidentally removed or
// renamed, this test will fail.
func TestAgentConfig_SandboxProfile_RoundTrip(t *testing.T) {
	trueBool := true
	original := AgentConfig{
		ID:             "test-agent",
		Name:           "Test Agent",
		SandboxProfile: SandboxProfileWorkspaceNet,
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

	if decoded.SandboxProfile != original.SandboxProfile {
		t.Errorf("SandboxProfile: got %q, want %q", decoded.SandboxProfile, original.SandboxProfile)
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

// TestAgentConfig_SandboxProfile_OmittedWhenEmpty confirms that omitempty
// suppresses sandbox_profile and shell_policy when they hold zero values,
// so existing configs without these fields are unaffected.
func TestAgentConfig_SandboxProfile_OmittedWhenEmpty(t *testing.T) {
	cfg := AgentConfig{ID: "minimal"}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	s := string(data)
	if strings.Contains(s, "sandbox_profile") {
		t.Errorf("sandbox_profile must be omitted when empty; got: %s", s)
	}
	if strings.Contains(s, "shell_policy") {
		t.Errorf("shell_policy must be omitted when nil; got: %s", s)
	}
}

// TestConfigMigration_LegacyMaixCamGracefullyIgnored verifies T28 from
// docs/specs/cancel-cross-channel-spec.md: a config.json that still contains a
// "maixcam" section under "channels" loads without panic, emits a structured
// Warn log entry, and leaves other channels intact.
func TestConfigMigration_LegacyMaixCamGracefullyIgnored(t *testing.T) {
	// Write a v1 config with a stale maixcam section and a live telegram section.
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	rawCfg := `{
		"version": 1,
		"channels": {
			"maixcam": {
				"enabled": true,
				"host": "192.168.1.42",
				"port": 9999
			},
			"telegram": {
				"enabled": true
			}
		}
	}`
	if err := os.WriteFile(cfgPath, []byte(rawCfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Capture slog output to assert the structured Warn is emitted.
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// Must not panic.
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig must not error on legacy maixcam section, got: %v", err)
	}

	// Telegram channel must still be loaded correctly.
	if !cfg.Channels.Telegram.Enabled {
		t.Error("telegram.enabled should be true; other channels must survive maixcam removal")
	}

	// A structured Warn mentioning "maixcam" must have been emitted.
	output := buf.String()
	if !strings.Contains(output, "maixcam") {
		t.Errorf("expected a Warn log entry containing %q for the legacy channel section; got output: %s",
			"maixcam", output)
	}
	if !strings.Contains(strings.ToLower(output), "warn") && !strings.Contains(output, `"level":"WARN"`) {
		t.Errorf("expected log level WARN in output; got: %s", output)
	}
}
