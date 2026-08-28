// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// setInst is a helper to safely mutate a channel instance using copy-assign.
func setInst(c *Config, key string, fn func(*ChannelInstanceConfig)) {
	if c.Channels == nil {
		c.Channels = make(map[string]ChannelInstanceConfig)
	}
	inst := c.Channels[key]
	fn(&inst)
	c.Channels[key] = inst
}

// TestChannelConfig_AllRefsRoundTrip verifies that every *Ref field on every
// channel and tool config struct survives a SaveConfig → LoadConfig round-trip
// without being dropped, mangled, or renamed. A regression in any JSON tag
// will cause the specific sub-test to fail.
func TestChannelConfig_AllRefsRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(cfg *Config)
		verify func(t *testing.T, cfg *Config)
	}{
		// --- Channel refs ---
		{
			name: "telegram/token_ref",
			setup: func(c *Config) {
				setInst(c, "telegram", func(inst *ChannelInstanceConfig) {
					inst.Enabled = true
					inst.TokenRef = "TG_TEST"
				})
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["telegram"].TokenRef != "TG_TEST" {
					t.Errorf("got %q, want TG_TEST", c.Channels["telegram"].TokenRef)
				}
			},
		},
		{
			name: "discord/token_ref",
			setup: func(c *Config) {
				setInst(c, "discord", func(inst *ChannelInstanceConfig) {
					inst.Enabled = true
					inst.TokenRef = "DISCORD_TOKEN_TEST"
				})
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["discord"].TokenRef != "DISCORD_TOKEN_TEST" {
					t.Errorf("got %q, want DISCORD_TOKEN_TEST", c.Channels["discord"].TokenRef)
				}
			},
		},
		{
			name: "slack/bot_token_ref",
			setup: func(c *Config) {
				setInst(c, "slack", func(inst *ChannelInstanceConfig) {
					inst.Enabled = true
					inst.BotTokenRef = "SLACK_BOT_TEST"
				})
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["slack"].BotTokenRef != "SLACK_BOT_TEST" {
					t.Errorf("got %q, want SLACK_BOT_TEST", c.Channels["slack"].BotTokenRef)
				}
			},
		},
		{
			name: "slack/app_token_ref",
			setup: func(c *Config) {
				setInst(c, "slack", func(inst *ChannelInstanceConfig) { inst.AppTokenRef = "SLACK_APP_TEST" })
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["slack"].AppTokenRef != "SLACK_APP_TEST" {
					t.Errorf("got %q, want SLACK_APP_TEST", c.Channels["slack"].AppTokenRef)
				}
			},
		},
		{
			name: "feishu/app_secret_ref",
			setup: func(c *Config) {
				setInst(c, "feishu", func(inst *ChannelInstanceConfig) {
					inst.Enabled = true
					inst.AppSecretRef = "FEISHU_APP_SECRET_TEST"
				})
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["feishu"].AppSecretRef != "FEISHU_APP_SECRET_TEST" {
					t.Errorf("got %q, want FEISHU_APP_SECRET_TEST", c.Channels["feishu"].AppSecretRef)
				}
			},
		},
		{
			name: "feishu/encrypt_key_ref",
			setup: func(c *Config) {
				setInst(
					c,
					"feishu",
					func(inst *ChannelInstanceConfig) { inst.EncryptKeyRef = "FEISHU_ENCRYPT_KEY_TEST" },
				)
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["feishu"].EncryptKeyRef != "FEISHU_ENCRYPT_KEY_TEST" {
					t.Errorf("got %q, want FEISHU_ENCRYPT_KEY_TEST", c.Channels["feishu"].EncryptKeyRef)
				}
			},
		},
		{
			name: "feishu/verification_token_ref",
			setup: func(c *Config) {
				setInst(c, "feishu", func(inst *ChannelInstanceConfig) { inst.VerificationTokenRef = "FEISHU_VT_TEST" })
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["feishu"].VerificationTokenRef != "FEISHU_VT_TEST" {
					t.Errorf("got %q, want FEISHU_VT_TEST", c.Channels["feishu"].VerificationTokenRef)
				}
			},
		},
		{
			name: "qq/app_secret_ref",
			setup: func(c *Config) {
				setInst(c, "qq", func(inst *ChannelInstanceConfig) { inst.AppSecretRef = "QQ_APP_SECRET_TEST" })
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["qq"].AppSecretRef != "QQ_APP_SECRET_TEST" {
					t.Errorf("got %q, want QQ_APP_SECRET_TEST", c.Channels["qq"].AppSecretRef)
				}
			},
		},
		{
			name: "dingtalk/client_secret_ref",
			setup: func(c *Config) {
				setInst(
					c,
					"dingtalk",
					func(inst *ChannelInstanceConfig) { inst.ClientSecretRef = "DINGTALK_SECRET_TEST" },
				)
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["dingtalk"].ClientSecretRef != "DINGTALK_SECRET_TEST" {
					t.Errorf("got %q, want DINGTALK_SECRET_TEST", c.Channels["dingtalk"].ClientSecretRef)
				}
			},
		},
		{
			name: "matrix/access_token_ref",
			setup: func(c *Config) {
				setInst(c, "matrix", func(inst *ChannelInstanceConfig) {
					inst.Enabled = true
					inst.AccessTokenRef = "MATRIX_TOKEN_TEST"
				})
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["matrix"].AccessTokenRef != "MATRIX_TOKEN_TEST" {
					t.Errorf("got %q, want MATRIX_TOKEN_TEST", c.Channels["matrix"].AccessTokenRef)
				}
			},
		},
		{
			name: "line/channel_secret_ref",
			setup: func(c *Config) {
				setInst(c, "line", func(inst *ChannelInstanceConfig) {
					inst.Enabled = true
					inst.ChannelSecretRef = "LINE_SECRET_TEST"
				})
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["line"].ChannelSecretRef != "LINE_SECRET_TEST" {
					t.Errorf("got %q, want LINE_SECRET_TEST", c.Channels["line"].ChannelSecretRef)
				}
			},
		},
		{
			name: "line/channel_access_token_ref",
			setup: func(c *Config) {
				setInst(
					c,
					"line",
					func(inst *ChannelInstanceConfig) { inst.ChannelAccessTokenRef = "LINE_ACCESS_TOKEN_TEST" },
				)
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["line"].ChannelAccessTokenRef != "LINE_ACCESS_TOKEN_TEST" {
					t.Errorf("got %q, want LINE_ACCESS_TOKEN_TEST", c.Channels["line"].ChannelAccessTokenRef)
				}
			},
		},
		{
			name: "wecom/secret_ref",
			setup: func(c *Config) {
				setInst(c, "wecom", func(inst *ChannelInstanceConfig) { inst.SecretRef = "WECOM_SECRET_TEST" })
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["wecom"].SecretRef != "WECOM_SECRET_TEST" {
					t.Errorf("got %q, want WECOM_SECRET_TEST", c.Channels["wecom"].SecretRef)
				}
			},
		},
		{
			name: "weixin/token_ref",
			setup: func(c *Config) {
				setInst(c, "weixin", func(inst *ChannelInstanceConfig) { inst.TokenRef = "WEIXIN_TOKEN_TEST" })
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["weixin"].TokenRef != "WEIXIN_TOKEN_TEST" {
					t.Errorf("got %q, want WEIXIN_TOKEN_TEST", c.Channels["weixin"].TokenRef)
				}
			},
		},
		{
			name: "irc/password_ref",
			setup: func(c *Config) {
				setInst(c, "irc", func(inst *ChannelInstanceConfig) { inst.PasswordRef = "IRC_PASSWORD_TEST" })
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["irc"].PasswordRef != "IRC_PASSWORD_TEST" {
					t.Errorf("got %q, want IRC_PASSWORD_TEST", c.Channels["irc"].PasswordRef)
				}
			},
		},
		{
			name: "irc/nickserv_password_ref",
			setup: func(c *Config) {
				setInst(c, "irc", func(inst *ChannelInstanceConfig) { inst.NickServPasswordRef = "IRC_NICKSERV_TEST" })
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["irc"].NickServPasswordRef != "IRC_NICKSERV_TEST" {
					t.Errorf("got %q, want IRC_NICKSERV_TEST", c.Channels["irc"].NickServPasswordRef)
				}
			},
		},
		{
			name: "irc/sasl_password_ref",
			setup: func(c *Config) {
				setInst(c, "irc", func(inst *ChannelInstanceConfig) { inst.SASLPasswordRef = "IRC_SASL_TEST" })
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels["irc"].SASLPasswordRef != "IRC_SASL_TEST" {
					t.Errorf("got %q, want IRC_SASL_TEST", c.Channels["irc"].SASLPasswordRef)
				}
			},
		},
		// --- Web tool refs ---
		{
			name: "web/brave/api_key_ref",
			setup: func(c *Config) {
				c.Tools.Web.Brave.APIKeyRef = "BRAVE_API_KEY_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Tools.Web.Brave.APIKeyRef != "BRAVE_API_KEY_TEST" {
					t.Errorf("got %q, want BRAVE_API_KEY_TEST", c.Tools.Web.Brave.APIKeyRef)
				}
			},
		},
		{
			name: "web/tavily/api_key_ref",
			setup: func(c *Config) {
				c.Tools.Web.Tavily.APIKeyRef = "TAVILY_API_KEY_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Tools.Web.Tavily.APIKeyRef != "TAVILY_API_KEY_TEST" {
					t.Errorf("got %q, want TAVILY_API_KEY_TEST", c.Tools.Web.Tavily.APIKeyRef)
				}
			},
		},
		{
			name: "web/perplexity/api_key_ref",
			setup: func(c *Config) {
				c.Tools.Web.Perplexity.APIKeyRef = "PERPLEXITY_API_KEY_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Tools.Web.Perplexity.APIKeyRef != "PERPLEXITY_API_KEY_TEST" {
					t.Errorf("got %q, want PERPLEXITY_API_KEY_TEST", c.Tools.Web.Perplexity.APIKeyRef)
				}
			},
		},
		{
			name: "web/glm_search/api_key_ref",
			setup: func(c *Config) {
				c.Tools.Web.GLMSearch.APIKeyRef = "GLM_API_KEY_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Tools.Web.GLMSearch.APIKeyRef != "GLM_API_KEY_TEST" {
					t.Errorf("got %q, want GLM_API_KEY_TEST", c.Tools.Web.GLMSearch.APIKeyRef)
				}
			},
		},
		{
			name: "web/baidu_search/api_key_ref",
			setup: func(c *Config) {
				c.Tools.Web.BaiduSearch.APIKeyRef = "BAIDU_API_KEY_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Tools.Web.BaiduSearch.APIKeyRef != "BAIDU_API_KEY_TEST" {
					t.Errorf("got %q, want BAIDU_API_KEY_TEST", c.Tools.Web.BaiduSearch.APIKeyRef)
				}
			},
		},
		// --- Voice ref ---
		{
			name: "voice/elevenlabs_api_key_ref",
			setup: func(c *Config) {
				c.Voice.ElevenLabsAPIKeyRef = "ELEVENLABS_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Voice.ElevenLabsAPIKeyRef != "ELEVENLABS_TEST" {
					t.Errorf("got %q, want ELEVENLABS_TEST", c.Voice.ElevenLabsAPIKeyRef)
				}
			},
		},
		// --- Skills refs ---
		{
			name: "skills/github/token_ref",
			setup: func(c *Config) {
				c.Tools.Skills.Marketplaces = []MarketplaceConfig{
					{Name: "github", Type: "github", Enabled: true, TokenRef: "GITHUB_TOKEN_TEST"},
				}
			},
			verify: func(t *testing.T, c *Config) {
				var ref string
				for _, m := range c.Tools.Skills.Marketplaces {
					if m.Type == "github" {
						ref = m.TokenRef
					}
				}
				if ref != "GITHUB_TOKEN_TEST" {
					t.Errorf("got %q, want GITHUB_TOKEN_TEST", ref)
				}
			},
		},
		{
			name: "skills/clawhub/auth_token_ref",
			setup: func(c *Config) {
				c.Tools.Skills.Marketplaces = []MarketplaceConfig{
					{Name: "clawhub", Type: "clawhub", Enabled: true, AuthTokenRef: "CLAWHUB_TOKEN_TEST"},
				}
			},
			verify: func(t *testing.T, c *Config) {
				var ref string
				for _, m := range c.Tools.Skills.Marketplaces {
					if m.Type == "clawhub" {
						ref = m.AuthTokenRef
					}
				}
				if ref != "CLAWHUB_TOKEN_TEST" {
					t.Errorf("got %q, want CLAWHUB_TOKEN_TEST", ref)
				}
			},
		},
		// --- Provider (ModelConfig) api_key_ref ---
		{
			name: "provider/api_key_ref",
			setup: func(c *Config) {
				c.Providers = []*ModelConfig{
					{Provider: "openai", Model: "gpt-4.1", APIKeyRef: "OPENAI_TEST_KEY"},
				}
			},
			verify: func(t *testing.T, c *Config) {
				if len(c.Providers) == 0 {
					t.Fatal("expected providers to be non-empty after round-trip")
				}
				if c.Providers[0].APIKeyRef != "OPENAI_TEST_KEY" {
					t.Errorf("got %q, want OPENAI_TEST_KEY", c.Providers[0].APIKeyRef)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Build a minimal v1 config, apply the setup, save, load back.
			cfg := DefaultConfig()
			tc.setup(cfg)

			path := filepath.Join(t.TempDir(), "config.json")
			if err := SaveConfig(path, cfg); err != nil {
				t.Fatalf("SaveConfig: %v", err)
			}

			// The saved file must be a v1 config — LoadConfig (no store) must work.
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			_ = data // available for debugging if needed

			loaded, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}

			tc.verify(t, loaded)
		})
	}
}
