// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
				c.Channels.Telegram.Enabled = true
				c.Channels.Telegram.TokenRef = "TG_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.Telegram.TokenRef != "TG_TEST" {
					t.Errorf("got %q, want TG_TEST", c.Channels.Telegram.TokenRef)
				}
			},
		},
		{
			name: "discord/token_ref",
			setup: func(c *Config) {
				c.Channels.Discord.Enabled = true
				c.Channels.Discord.TokenRef = "DISCORD_TOKEN_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.Discord.TokenRef != "DISCORD_TOKEN_TEST" {
					t.Errorf("got %q, want DISCORD_TOKEN_TEST", c.Channels.Discord.TokenRef)
				}
			},
		},
		{
			name: "slack/bot_token_ref",
			setup: func(c *Config) {
				c.Channels.Slack.Enabled = true
				c.Channels.Slack.BotTokenRef = "SLACK_BOT_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.Slack.BotTokenRef != "SLACK_BOT_TEST" {
					t.Errorf("got %q, want SLACK_BOT_TEST", c.Channels.Slack.BotTokenRef)
				}
			},
		},
		{
			name: "slack/app_token_ref",
			setup: func(c *Config) {
				c.Channels.Slack.AppTokenRef = "SLACK_APP_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.Slack.AppTokenRef != "SLACK_APP_TEST" {
					t.Errorf("got %q, want SLACK_APP_TEST", c.Channels.Slack.AppTokenRef)
				}
			},
		},
		{
			name: "feishu/app_secret_ref",
			setup: func(c *Config) {
				c.Channels.Feishu.Enabled = true
				c.Channels.Feishu.AppSecretRef = "FEISHU_APP_SECRET_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.Feishu.AppSecretRef != "FEISHU_APP_SECRET_TEST" {
					t.Errorf("got %q, want FEISHU_APP_SECRET_TEST", c.Channels.Feishu.AppSecretRef)
				}
			},
		},
		{
			name: "feishu/encrypt_key_ref",
			setup: func(c *Config) {
				c.Channels.Feishu.EncryptKeyRef = "FEISHU_ENCRYPT_KEY_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.Feishu.EncryptKeyRef != "FEISHU_ENCRYPT_KEY_TEST" {
					t.Errorf("got %q, want FEISHU_ENCRYPT_KEY_TEST", c.Channels.Feishu.EncryptKeyRef)
				}
			},
		},
		{
			name: "feishu/verification_token_ref",
			setup: func(c *Config) {
				c.Channels.Feishu.VerificationTokenRef = "FEISHU_VT_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.Feishu.VerificationTokenRef != "FEISHU_VT_TEST" {
					t.Errorf("got %q, want FEISHU_VT_TEST", c.Channels.Feishu.VerificationTokenRef)
				}
			},
		},
		{
			name: "qq/app_secret_ref",
			setup: func(c *Config) {
				c.Channels.QQ.AppSecretRef = "QQ_APP_SECRET_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.QQ.AppSecretRef != "QQ_APP_SECRET_TEST" {
					t.Errorf("got %q, want QQ_APP_SECRET_TEST", c.Channels.QQ.AppSecretRef)
				}
			},
		},
		{
			name: "dingtalk/client_secret_ref",
			setup: func(c *Config) {
				c.Channels.DingTalk.ClientSecretRef = "DINGTALK_SECRET_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.DingTalk.ClientSecretRef != "DINGTALK_SECRET_TEST" {
					t.Errorf("got %q, want DINGTALK_SECRET_TEST", c.Channels.DingTalk.ClientSecretRef)
				}
			},
		},
		{
			name: "matrix/access_token_ref",
			setup: func(c *Config) {
				c.Channels.Matrix.Enabled = true
				c.Channels.Matrix.AccessTokenRef = "MATRIX_TOKEN_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.Matrix.AccessTokenRef != "MATRIX_TOKEN_TEST" {
					t.Errorf("got %q, want MATRIX_TOKEN_TEST", c.Channels.Matrix.AccessTokenRef)
				}
			},
		},
		{
			name: "line/channel_secret_ref",
			setup: func(c *Config) {
				c.Channels.LINE.Enabled = true
				c.Channels.LINE.ChannelSecretRef = "LINE_SECRET_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.LINE.ChannelSecretRef != "LINE_SECRET_TEST" {
					t.Errorf("got %q, want LINE_SECRET_TEST", c.Channels.LINE.ChannelSecretRef)
				}
			},
		},
		{
			name: "line/channel_access_token_ref",
			setup: func(c *Config) {
				c.Channels.LINE.ChannelAccessTokenRef = "LINE_ACCESS_TOKEN_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.LINE.ChannelAccessTokenRef != "LINE_ACCESS_TOKEN_TEST" {
					t.Errorf("got %q, want LINE_ACCESS_TOKEN_TEST", c.Channels.LINE.ChannelAccessTokenRef)
				}
			},
		},
		{
			name: "wecom/secret_ref",
			setup: func(c *Config) {
				c.Channels.WeCom.SecretRef = "WECOM_SECRET_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.WeCom.SecretRef != "WECOM_SECRET_TEST" {
					t.Errorf("got %q, want WECOM_SECRET_TEST", c.Channels.WeCom.SecretRef)
				}
			},
		},
		{
			name: "weixin/token_ref",
			setup: func(c *Config) {
				c.Channels.Weixin.TokenRef = "WEIXIN_TOKEN_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.Weixin.TokenRef != "WEIXIN_TOKEN_TEST" {
					t.Errorf("got %q, want WEIXIN_TOKEN_TEST", c.Channels.Weixin.TokenRef)
				}
			},
		},
		{
			name: "irc/password_ref",
			setup: func(c *Config) {
				c.Channels.IRC.PasswordRef = "IRC_PASSWORD_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.IRC.PasswordRef != "IRC_PASSWORD_TEST" {
					t.Errorf("got %q, want IRC_PASSWORD_TEST", c.Channels.IRC.PasswordRef)
				}
			},
		},
		{
			name: "irc/nickserv_password_ref",
			setup: func(c *Config) {
				c.Channels.IRC.NickServPasswordRef = "IRC_NICKSERV_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.IRC.NickServPasswordRef != "IRC_NICKSERV_TEST" {
					t.Errorf("got %q, want IRC_NICKSERV_TEST", c.Channels.IRC.NickServPasswordRef)
				}
			},
		},
		{
			name: "irc/sasl_password_ref",
			setup: func(c *Config) {
				c.Channels.IRC.SASLPasswordRef = "IRC_SASL_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Channels.IRC.SASLPasswordRef != "IRC_SASL_TEST" {
					t.Errorf("got %q, want IRC_SASL_TEST", c.Channels.IRC.SASLPasswordRef)
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
				c.Tools.Skills.Github.TokenRef = "GITHUB_TOKEN_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Tools.Skills.Github.TokenRef != "GITHUB_TOKEN_TEST" {
					t.Errorf("got %q, want GITHUB_TOKEN_TEST", c.Tools.Skills.Github.TokenRef)
				}
			},
		},
		{
			name: "skills/clawhub/auth_token_ref",
			setup: func(c *Config) {
				c.Tools.Skills.Registries.ClawHub.AuthTokenRef = "CLAWHUB_TOKEN_TEST"
			},
			verify: func(t *testing.T, c *Config) {
				if c.Tools.Skills.Registries.ClawHub.AuthTokenRef != "CLAWHUB_TOKEN_TEST" {
					t.Errorf("got %q, want CLAWHUB_TOKEN_TEST", c.Tools.Skills.Registries.ClawHub.AuthTokenRef)
				}
			},
		},
		// --- Provider (ModelConfig) api_key_ref ---
		{
			name: "provider/api_key_ref",
			setup: func(c *Config) {
				c.Providers = []*ModelConfig{
					{ModelName: "test-model", Model: "openai/gpt-4o", APIKeyRef: "OPENAI_TEST_KEY"},
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
