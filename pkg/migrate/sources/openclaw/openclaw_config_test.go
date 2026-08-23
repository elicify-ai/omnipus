package openclaw

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestLoadOpenClawConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "openclaw.json")

	testConfig := `{
		"agents": {
			"defaults": {
				"model": {
					"primary": "anthropic/claude-sonnet-4-20250514"
				},
				"workspace": "~/.openclaw/workspace"
			},
			"list": [
				{
					"id": "main",
					"name": "Main Agent",
					"model": {
						"primary": "openai/gpt-4o",
						"fallbacks": ["claude-3-opus"]
					}
				}
			]
		},
		"channels": {
			"telegram": {
				"enabled": true,
				"botToken": "test-token",
				"allowFrom": ["user1", "user2"]
			},
			"discord": {
				"enabled": true,
				"token": "discord-token"
			}
		},
		"models": {
			"providers": {
				"anthropic": {
					"api_key": "sk-ant-test",
					"base_url": "https://api.anthropic.com"
				},
				"openai": {
					"api_key": "sk-test"
				}
			}
		}
	}`

	err := os.WriteFile(configPath, []byte(testConfig), 0o644)
	if err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadOpenClawConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Agents == nil {
		t.Error("agents should not be nil")
	}

	if cfg.Agents.Defaults == nil {
		t.Error("agents.defaults should not be nil")
	}

	provider, model := cfg.GetDefaultModel()
	if provider != "anthropic" {
		t.Errorf("expected provider 'anthropic', got '%s'", provider)
	}
	if model != "claude-sonnet-4-20250514" {
		t.Errorf("expected model 'claude-sonnet-4-20250514', got '%s'", model)
	}

	workspace := cfg.GetDefaultWorkspace()
	if workspace != "~/.omnipus/workspace" {
		t.Errorf("expected workspace '~/.omnipus/workspace', got '%s'", workspace)
	}

	agents := cfg.GetAgents()
	if len(agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].ID != "main" {
		t.Errorf("expected agent id 'main', got '%s'", agents[0].ID)
	}

	if cfg.Channels == nil {
		t.Error("channels should not be nil")
	}
	if cfg.Channels.Telegram == nil {
		t.Error("telegram channel should not be nil")
	}
	if cfg.Channels.Telegram.BotToken == nil || *cfg.Channels.Telegram.BotToken != "test-token" {
		t.Error("telegram bot token not parsed correctly")
	}
}

func TestGetProviderConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "openclaw.json")

	testConfig := `{
		"models": {
			"providers": {
				"anthropic": {
					"api_key": "sk-ant-test",
					"base_url": "https://api.anthropic.com",
					"max_tokens": 4096
				},
				"openai": {
					"api_key": "sk-test",
					"base_url": "https://api.openai.com"
				}
			}
		}
	}`

	err := os.WriteFile(configPath, []byte(testConfig), 0o644)
	if err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadOpenClawConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	providers := GetProviderConfig(cfg.Models)
	if len(providers) != 2 {
		t.Errorf("expected 2 providers, got %d", len(providers))
	}

	if anthropic, ok := providers["anthropic"]; ok {
		if anthropic.APIKey != "sk-ant-test" {
			t.Errorf("expected anthropic api_key 'sk-ant-test', got '%s'", anthropic.APIKey)
		}
		if anthropic.BaseURL != "https://api.anthropic.com" {
			t.Errorf("expected anthropic base_url 'https://api.anthropic.com', got '%s'", anthropic.BaseURL)
		}
	} else {
		t.Error("anthropic provider not found")
	}

	if openai, ok := providers["openai"]; ok {
		if openai.APIKey != "sk-test" {
			t.Errorf("expected openai api_key 'sk-test', got '%s'", openai.APIKey)
		}
	} else {
		t.Error("openai provider not found")
	}
}

func TestConvertToOmnipus(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "openclaw.json")

	testConfig := `{
		"agents": {
			"defaults": {
				"model": {
					"primary": "anthropic/claude-sonnet-4-20250514"
				},
				"workspace": "~/.openclaw/workspace"
			},
			"list": [
				{
					"id": "main",
					"name": "Main Agent"
				},
				{
					"id": "assistant",
					"name": "Assistant",
					"skills": ["skill1", "skill2"]
				}
			]
		},
		"channels": {
			"telegram": {
				"enabled": true,
				"botToken": "test-token",
				"allowFrom": ["user1", "user2"]
			},
			"discord": {
				"enabled": false,
				"token": "discord-token"
			},
			"whatsapp": {
				"enabled": true,
				"bridgeUrl": "http://localhost:3000"
			},
			"feishu": {
				"enabled": true,
				"appId": "app-id",
				"appSecret": "app-secret",
				"allowFrom": ["user3"]
			},
			"signal": {
				"enabled": true
			}
		},
		"models": {
			"providers": {
				"anthropic": {
					"api_key": "sk-ant-test"
				},
				"openai": {
					"api_key": "sk-test"
				}
			}
		},
		"skills": {
			"entries": {
				"skill1": {}
			}
		},
		"memory": {"enabled": true},
		"cron": {"enabled": true}
	}`

	err := os.WriteFile(configPath, []byte(testConfig), 0o644)
	if err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadOpenClawConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	omnipusCfg, warnings, err := cfg.ConvertToOmnipus("")
	if err != nil {
		t.Fatalf("failed to convert config: %v", err)
	}

	if omnipusCfg.Agents.Defaults.ModelName != "claude-sonnet-4-20250514" {
		t.Errorf("expected model 'claude-sonnet-4-20250514', got '%s'", omnipusCfg.Agents.Defaults.ModelName)
	}
	if omnipusCfg.Agents.Defaults.Workspace != "~/.omnipus/workspace" {
		t.Errorf("expected workspace '~/.omnipus/workspace', got '%s'", omnipusCfg.Agents.Defaults.Workspace)
	}

	if len(omnipusCfg.Agents.List) != 2 {
		t.Errorf("expected 2 agents, got %d", len(omnipusCfg.Agents.List))
	}
	if omnipusCfg.Agents.List[0].ID != "main" {
		t.Errorf("expected first agent id 'main', got '%s'", omnipusCfg.Agents.List[0].ID)
	}
	if omnipusCfg.Agents.List[1].Skills == nil || len(omnipusCfg.Agents.List[1].Skills) != 2 {
		t.Errorf("expected 2 skills for assistant agent")
	}

	if !omnipusCfg.Channels.Telegram.Enabled {
		t.Error("telegram should be enabled")
	}
	if omnipusCfg.Channels.Telegram.Token != "test-token" {
		t.Errorf("expected telegram token 'test-token', got '%s'", omnipusCfg.Channels.Telegram.Token)
	}

	// BridgeURL is no longer migrated (WhatsApp is always native; bridge removed).
	// Verify the channel's enabled flag still migrates.
	if !omnipusCfg.Channels.WhatsApp.Enabled {
		t.Error("whatsapp should be enabled after migration")
	}

	if omnipusCfg.Channels.Feishu.AppID != "app-id" {
		t.Errorf("expected feishu app ID 'app-id', got '%s'", omnipusCfg.Channels.Feishu.AppID)
	}

	if len(omnipusCfg.Providers) != 1 {
		t.Errorf("expected 1 model config (no models.json provided), got %d", len(omnipusCfg.Providers))
	}

	foundWarning := false
	for _, w := range warnings {
		if len(w) > 0 {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Log("warnings should be generated for skills, memory, cron, and unsupported channels")
	}
}

func TestConvertToOmnipusWithQQAndDingTalk(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "openclaw.json")

	testConfig := `{
		"agents": {
			"defaults": {
				"model": {
					"primary": "anthropic/claude-sonnet-4-20250514"
				}
			}
		},
		"channels": {
			"qq": {
				"enabled": true,
				"appId": "qq-app-id",
				"appSecret": "qq-app-secret"
			},
			"dingtalk": {
				"enabled": true,
				"appId": "ding-app-id",
				"appSecret": "ding-app-secret"
			},

			"slack": {
				"enabled": true,
				"botToken": "xoxb-test",
				"appToken": "xapp-test"
			}
		}
	}`

	err := os.WriteFile(configPath, []byte(testConfig), 0o644)
	if err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadOpenClawConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	omnipusCfg, _, err := cfg.ConvertToOmnipus("")
	if err != nil {
		t.Fatalf("failed to convert config: %v", err)
	}

	if !omnipusCfg.Channels.QQ.Enabled {
		t.Error("qq should be enabled")
	}
	if omnipusCfg.Channels.QQ.AppID != "qq-app-id" {
		t.Errorf("expected qq app ID 'qq-app-id', got '%s'", omnipusCfg.Channels.QQ.AppID)
	}

	if !omnipusCfg.Channels.DingTalk.Enabled {
		t.Error("dingtalk should be enabled")
	}
	if omnipusCfg.Channels.DingTalk.ClientID != "ding-app-id" {
		t.Errorf("expected dingtalk client ID 'ding-app-id', got '%s'", omnipusCfg.Channels.DingTalk.ClientID)
	}

	if !omnipusCfg.Channels.Slack.Enabled {
		t.Error("slack should be enabled")
	}
	if omnipusCfg.Channels.Slack.BotToken != "xoxb-test" {
		t.Errorf("expected slack bot token 'xoxb-test', got '%s'", omnipusCfg.Channels.Slack.BotToken)
	}
	if omnipusCfg.Channels.Slack.AppToken != "xapp-test" {
		t.Errorf("expected slack app token 'xapp-test', got '%s'", omnipusCfg.Channels.Slack.AppToken)
	}
}

func TestConvertToOmnipusWithMatrix(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "openclaw.json")

	testConfig := `{
		"channels": {
			"matrix": {
				"enabled": true,
				"homeserver": "https://matrix.example.com",
				"userId": "@bot:matrix.example.com",
				"accessToken": "syt_test_token",
				"allowFrom": ["@alice:matrix.example.com"]
			}
		}
	}`

	err := os.WriteFile(configPath, []byte(testConfig), 0o644)
	if err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadOpenClawConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	omnipusCfg, warnings, err := cfg.ConvertToOmnipus("")
	if err != nil {
		t.Fatalf("failed to convert config: %v", err)
	}

	if !omnipusCfg.Channels.Matrix.Enabled {
		t.Error("matrix should be enabled")
	}
	if omnipusCfg.Channels.Matrix.Homeserver != "https://matrix.example.com" {
		t.Errorf("expected matrix homeserver, got %q", omnipusCfg.Channels.Matrix.Homeserver)
	}
	if omnipusCfg.Channels.Matrix.UserID != "@bot:matrix.example.com" {
		t.Errorf("expected matrix user_id, got %q", omnipusCfg.Channels.Matrix.UserID)
	}
	if omnipusCfg.Channels.Matrix.AccessToken != "syt_test_token" {
		t.Errorf("expected matrix access_token, got %q", omnipusCfg.Channels.Matrix.AccessToken)
	}
	if len(omnipusCfg.Channels.Matrix.AllowFrom) != 1 ||
		omnipusCfg.Channels.Matrix.AllowFrom[0] != "@alice:matrix.example.com" {
		t.Errorf("unexpected matrix allow_from: %#v", omnipusCfg.Channels.Matrix.AllowFrom)
	}

	for _, w := range warnings {
		if strings.Contains(w, "Channel 'matrix'") {
			t.Fatalf("matrix should no longer be reported as unsupported, warning=%q", w)
		}
	}
}

func TestConvertToOmnipusWithMatrixDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "openclaw.json")

	testConfig := `{
		"channels": {
			"matrix": {
				"enabled": false,
				"homeserver": "https://matrix.example.com",
				"userId": "@bot:matrix.example.com",
				"accessToken": "syt_test_token"
			}
		}
	}`

	err := os.WriteFile(configPath, []byte(testConfig), 0o644)
	if err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadOpenClawConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	omnipusCfg, _, err := cfg.ConvertToOmnipus("")
	if err != nil {
		t.Fatalf("failed to convert config: %v", err)
	}

	if omnipusCfg.Channels.Matrix.Enabled {
		t.Error("matrix should respect enabled=false from source config")
	}
}

func TestOpenClawAgentModel(t *testing.T) {
	model := &OpenClawAgentModel{
		Primary:   strPtr("anthropic/claude-3-opus"),
		Fallbacks: []string{"claude-3-sonnet", "claude-3-haiku"},
	}

	primary := model.GetPrimary()
	if primary != "anthropic/claude-3-opus" {
		t.Errorf("expected primary 'anthropic/claude-3-opus', got '%s'", primary)
	}

	fallbacks := model.GetFallbacks()
	if len(fallbacks) != 2 {
		t.Errorf("expected 2 fallbacks, got %d", len(fallbacks))
	}

	model2 := &OpenClawAgentModel{
		Simple: "claude-3-opus",
	}

	primary2 := model2.GetPrimary()
	if primary2 != "claude-3-opus" {
		t.Errorf("expected primary 'claude-3-opus' from Simple, got '%s'", primary2)
	}
}

func TestChannelEnabled(t *testing.T) {
	cfg := &OpenClawConfig{
		Channels: &OpenClawChannels{
			Telegram: &OpenClawTelegramConfig{
				Enabled: boolPtr(true),
			},
			Discord: &OpenClawDiscordConfig{
				Enabled: boolPtr(false),
			},
			Slack: &OpenClawSlackConfig{
				Enabled: boolPtr(true),
			},
		},
	}

	if !cfg.IsChannelEnabled("telegram") {
		t.Error("telegram should be enabled")
	}
	if cfg.IsChannelEnabled("discord") {
		t.Error("discord should be disabled")
	}
	if !cfg.IsChannelEnabled("slack") {
		t.Error("slack should be enabled (explicitly set)")
	}
	if !cfg.IsChannelEnabled("matrix") {
		t.Error("matrix should be enabled (nil config defaults to enabled)")
	}
	if cfg.IsChannelEnabled("line") {
		t.Error("line should return false (not in switch cases)")
	}
}

func TestGetDefaultModel(t *testing.T) {
	cfg := &OpenClawConfig{
		Agents: &OpenClawAgents{
			Defaults: &OpenClawAgentDefaults{
				Model: &OpenClawAgentModel{
					Primary: strPtr("openai/gpt-4"),
				},
			},
		},
	}

	provider, model := cfg.GetDefaultModel()
	if provider != "openai" {
		t.Errorf("expected provider 'openai', got '%s'", provider)
	}
	if model != "gpt-4" {
		t.Errorf("expected model 'gpt-4', got '%s'", model)
	}
}

func TestGetDefaultModelWithNoDefaults(t *testing.T) {
	cfg := &OpenClawConfig{}

	provider, model := cfg.GetDefaultModel()
	if provider != "anthropic" {
		t.Errorf("expected default provider 'anthropic', got '%s'", provider)
	}
	if model != "claude-sonnet-4-20250514" {
		t.Errorf("expected default model 'claude-sonnet-4-20250514', got '%s'", model)
	}
}

func TestHasFunctions(t *testing.T) {
	cfg := &OpenClawConfig{
		Skills:  &OpenClawSkills{Entries: map[string]json.RawMessage{"skill1": nil}},
		Memory:  json.RawMessage(`{"enabled": true}`),
		Cron:    json.RawMessage(`{"enabled": true}`),
		Hooks:   json.RawMessage(`{"enabled": true}`),
		Session: json.RawMessage(`{"enabled": true}`),
		Auth:    &OpenClawAuth{Profiles: json.RawMessage(`{"profile1": {}}`)},
	}

	if !cfg.HasSkills() {
		t.Error("should have skills")
	}
	if !cfg.HasMemory() {
		t.Error("should have memory")
	}
	if !cfg.HasCron() {
		t.Error("should have cron")
	}
	if !cfg.HasHooks() {
		t.Error("should have hooks")
	}
	if !cfg.HasSession() {
		t.Error("should have session")
	}
	if !cfg.HasAuthProfiles() {
		t.Error("should have auth profiles")
	}

	cfg2 := &OpenClawConfig{}
	if cfg2.HasSkills() {
		t.Error("should not have skills")
	}
	if cfg2.HasMemory() {
		t.Error("should not have memory")
	}
}

func TestLoadOpenClawConfigFromDir(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "openclaw.json")

	testConfig := `{"agents": {}}`
	err := os.WriteFile(configPath, []byte(testConfig), 0o644)
	if err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadOpenClawConfigFromDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to load config from dir: %v", err)
	}

	if cfg.Agents == nil {
		t.Error("agents should not be nil")
	}

	_, err = LoadOpenClawConfigFromDir("/nonexistent/dir")
	if err == nil {
		t.Error("should return error for nonexistent dir")
	}
}

func TestToStandardConfig(t *testing.T) {
	omnipusCfg := &OmnipusConfig{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Provider:  "anthropic",
				ModelName: "claude-sonnet-4-20250514",
				Workspace: "~/.omnipus/workspace",
			},
			List: []AgentConfig{
				{
					ID:      "main",
					Name:    "Main Agent",
					Default: true,
				},
			},
		},
		Providers: []ModelConfig{
			{
				ModelName: "claude-sonnet-4-20250514",
				Model:     "anthropic/claude-sonnet-4-20250514",
				APIKey:    "sk-ant-test",
			},
		},
		Channels: ChannelsConfig{
			Telegram: TelegramConfig{
				Enabled:   true,
				Token:     "test-token",
				AllowFrom: []string{"user1"},
			},
			WhatsApp: WhatsAppConfig{
				Enabled: true,
			},
		},
		Gateway: GatewayConfig{
			Host: "0.0.0.0",
			Port: 8080,
		},
	}

	stdCfg := omnipusCfg.ToStandardConfig()

	// ADR-068 D14.1: the standard config carries the default as the exact pair.
	wantDefault := config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4-20250514"}
	if stdCfg.Agents.Defaults.DefaultModel != wantDefault {
		t.Errorf("expected default_model %+v, got %+v", wantDefault, stdCfg.Agents.Defaults.DefaultModel)
	}
	if stdCfg.Agents.Defaults.Home != "~/.omnipus/workspace" {
		t.Errorf("expected workspace '~/.omnipus/workspace', got '%s'", stdCfg.Agents.Defaults.Home)
	}

	if len(stdCfg.Agents.List) != 1 {
		t.Errorf("expected 1 agent, got %d", len(stdCfg.Agents.List))
	}
	if stdCfg.Agents.List[0].ID != "main" {
		t.Errorf("expected agent id 'main', got '%s'", stdCfg.Agents.List[0].ID)
	}

	foundModel := false
	var foundAPIKey string
	for _, m := range stdCfg.Providers {
		if m.ModelName == "claude-sonnet-4-20250514" {
			foundModel = true
			foundAPIKey = m.APIKey()
			break
		}
	}
	if !foundModel {
		t.Error("expected to find claude-sonnet-4-20250514 model config")
	}
	// API keys are not migrated from OpenClaw; the credential store is separate.
	// Users must re-enter them via `omnipus credentials set <REF> <value>`.
	if foundAPIKey != "" {
		t.Errorf("expected empty api key after migration (secrets not migrated), got '%s'", foundAPIKey)
	}

	if !stdCfg.Channels["telegram"].Enabled {
		t.Error("telegram should be enabled")
	}
	// Secrets are not migrated from OpenClaw; users must re-enter them via
	// `omnipus credentials set TELEGRAM_TOKEN <value>` and set token_ref in config.
	if stdCfg.Channels["telegram"].TokenRef != "" {
		t.Errorf(
			"expected empty token_ref after migration (secrets not migrated), got %q",
			stdCfg.Channels["telegram"].TokenRef,
		)
	}

	if stdCfg.Gateway.Port != 8080 {
		t.Errorf("expected gateway port 8080, got %d", stdCfg.Gateway.Port)
	}
}

// TestToStandardConfig_ExecDenyPatterns_MigrateToShellDenyPatterns verifies
// that ToStandardConfig routes a legacy OpenClaw exec deny-pattern config
// into the fields the ADR-036 `bash` tool actually reads
// (Sandbox.ShellDenyPatterns + per-agent AgentConfig.ShellPolicy) instead of
// the dead config.ExecConfig. Also verifies that every migrated agent's
// ShellPolicy.EnableDenyPatterns is set to true so the global patterns are
// not left inert under the new tool's per-agent opt-in default (mirroring
// OpenClaw's old single global on/off switch).
func TestToStandardConfig_ExecDenyPatterns_MigrateToShellDenyPatterns(t *testing.T) {
	omnipusCfg := &OmnipusConfig{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{ID: "main", Name: "Main Agent", Default: true},
				{ID: "helper", Name: "Helper Agent"},
			},
		},
		Tools: ToolsConfig{
			Exec: ExecConfig{
				EnableDenyPatterns: true,
				CustomDenyPatterns: []string{`^\s*rm\s+-rf\s+/`, `curl.*\|.*sh`},
			},
		},
	}

	stdCfg := omnipusCfg.ToStandardConfig()

	// The live global field must carry the migrated patterns.
	if len(stdCfg.Sandbox.ShellDenyPatterns) != 2 {
		t.Fatalf(
			"expected 2 patterns in Sandbox.ShellDenyPatterns, got %d: %v",
			len(stdCfg.Sandbox.ShellDenyPatterns), stdCfg.Sandbox.ShellDenyPatterns,
		)
	}
	wantPatterns := map[string]bool{`^\s*rm\s+-rf\s+/`: true, `curl.*\|.*sh`: true}
	for _, p := range stdCfg.Sandbox.ShellDenyPatterns {
		if !wantPatterns[p] {
			t.Errorf("unexpected pattern in Sandbox.ShellDenyPatterns: %q", p)
		}
	}

	// Every migrated agent must be opted into the per-agent enable flag —
	// otherwise the global patterns above sit inert (new tool default: off).
	if len(stdCfg.Agents.List) != 2 {
		t.Fatalf("expected 2 migrated agents, got %d", len(stdCfg.Agents.List))
	}
	for _, a := range stdCfg.Agents.List {
		if a.ShellPolicy == nil {
			t.Errorf("agent %q: ShellPolicy must be non-nil after exec deny-pattern migration", a.ID)
			continue
		}
		if !a.ShellPolicy.EnableDenyPatterns {
			t.Errorf("agent %q: ShellPolicy.EnableDenyPatterns must be true", a.ID)
		}
	}
}

// TestToStandardConfig_ExecDenyPatterns_DisabledNotMigrated verifies that
// when the legacy OpenClaw config had deny patterns disabled
// (EnableDenyPatterns=false, matching OpenClaw's old semantics of "patterns
// configured but the feature switched off"), nothing is migrated — no
// inert global patterns, no per-agent opt-in.
func TestToStandardConfig_ExecDenyPatterns_DisabledNotMigrated(t *testing.T) {
	omnipusCfg := &OmnipusConfig{
		Agents: AgentsConfig{
			List: []AgentConfig{{ID: "main", Name: "Main Agent"}},
		},
		Tools: ToolsConfig{
			Exec: ExecConfig{
				EnableDenyPatterns: false,
				CustomDenyPatterns: []string{`curl.*\|.*sh`},
			},
		},
	}

	stdCfg := omnipusCfg.ToStandardConfig()

	if len(stdCfg.Sandbox.ShellDenyPatterns) != 0 {
		t.Errorf(
			"expected no migrated patterns when EnableDenyPatterns=false, got %v",
			stdCfg.Sandbox.ShellDenyPatterns,
		)
	}
	if stdCfg.Agents.List[0].ShellPolicy != nil {
		t.Errorf(
			"expected no ShellPolicy set when EnableDenyPatterns=false, got %+v",
			stdCfg.Agents.List[0].ShellPolicy,
		)
	}
}

func TestLoadProviderConfigFromAgentsDir(t *testing.T) {
	tmpDir := t.TempDir()

	agentsDir := filepath.Join(tmpDir, "agents", "main", "agent")
	err := os.MkdirAll(agentsDir, 0o755)
	if err != nil {
		t.Fatalf("failed to create agents dir: %v", err)
	}

	modelsJSON := `{
		"providers": {
			"anthropic": {
				"baseUrl": "https://api.anthropic.com",
				"api": "anthropic",
				"apiKey": "sk-ant-from-models",
				"models": [
					{
						"id": "claude-sonnet-4-20250514",
						"name": "Claude Sonnet 4"
					}
				]
			},
			"openai": {
				"baseUrl": "https://api.openai.com",
				"api": "openai",
				"apiKey": "sk-from-models",
				"models": [
					{
						"id": "gpt-4o",
						"name": "GPT-4o"
					}
				]
			},
			"zhipu": {
				"baseUrl": "https://open.bigmodel.cn/api/paas/v4",
				"api": "openai",
				"apiKey": "zhipu-key",
				"models": []
			}
		}
	}`

	err = os.WriteFile(filepath.Join(agentsDir, "models.json"), []byte(modelsJSON), 0o644)
	if err != nil {
		t.Fatalf("failed to write models.json: %v", err)
	}

	providers := GetProviderConfigFromDir(tmpDir)
	if len(providers) != 3 {
		t.Errorf("expected 3 providers, got %d", len(providers))
	}

	if anthropic, ok := providers["anthropic"]; ok {
		if anthropic.ApiKey != "sk-ant-from-models" {
			t.Errorf("expected anthropic apiKey 'sk-ant-from-models', got '%s'", anthropic.ApiKey)
		}
		if anthropic.BaseUrl != "https://api.anthropic.com" {
			t.Errorf("expected anthropic baseUrl 'https://api.anthropic.com', got '%s'", anthropic.BaseUrl)
		}
	} else {
		t.Error("anthropic provider not found")
	}

	if openai, ok := providers["openai"]; ok {
		if openai.ApiKey != "sk-from-models" {
			t.Errorf("expected openai apiKey 'sk-from-models', got '%s'", openai.ApiKey)
		}
		if openai.BaseUrl != "https://api.openai.com" {
			t.Errorf("expected openai baseUrl 'https://api.openai.com', got '%s'", openai.BaseUrl)
		}
	} else {
		t.Error("openai provider not found")
	}

	if zhipu, ok := providers["zhipu"]; ok {
		if zhipu.ApiKey != "zhipu-key" {
			t.Errorf("expected zhipu apiKey 'zhipu-key', got '%s'", zhipu.ApiKey)
		}
		if zhipu.BaseUrl != "https://open.bigmodel.cn/api/paas/v4" {
			t.Errorf("expected zhipu baseUrl 'https://open.bigmodel.cn/api/paas/v4', got '%s'", zhipu.BaseUrl)
		}
	} else {
		t.Error("zhipu provider not found")
	}
}

func TestGetProviderConfigFromDirNotExist(t *testing.T) {
	providers := GetProviderConfigFromDir("/nonexistent/path")
	if len(providers) != 0 {
		t.Errorf("expected 0 providers for nonexistent path, got %d", len(providers))
	}
}

func strPtr(s string) *string {
	return &s
}

func boolPtr(b bool) *bool {
	return &b
}
