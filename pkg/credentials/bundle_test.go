// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials_test

import (
	"encoding/hex"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

// TestResolveBundle_AllChannelRefs is a drift guard: it seeds the store with a
// known plaintext for every channel ref field, calls ResolveBundle, and asserts
// the bundle contains exactly the expected value. If a new channel is added
// without its refs being registered in ResolveAll, this test will notice because
// the bundle will not contain the expected key.
func TestResolveBundle_AllChannelRefs(t *testing.T) {
	type refCase struct {
		refName  string
		want     string
		setOnCfg func(cfg *config.Config, refName string)
	}

	cases := []refCase{
		{
			refName: "TELEGRAM_TOKEN",
			want:    "tg-bot-secret",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.Telegram.TokenRef = ref
			},
		},
		{
			refName: "DISCORD_TOKEN",
			want:    "discord-secret",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.Discord.TokenRef = ref
			},
		},
		{
			refName: "SLACK_BOT_TOKEN",
			want:    "xoxb-bot-token",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.Slack.BotTokenRef = ref
			},
		},
		{
			refName: "SLACK_APP_TOKEN",
			want:    "xapp-app-token",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.Slack.AppTokenRef = ref
			},
		},
		{
			refName: "FEISHU_APP_SECRET",
			want:    "feishu-app-secret",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.Feishu.AppSecretRef = ref
			},
		},
		{
			refName: "FEISHU_ENCRYPT_KEY",
			want:    "feishu-encrypt-key",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.Feishu.EncryptKeyRef = ref
			},
		},
		{
			refName: "FEISHU_VERIFICATION_TOKEN",
			want:    "feishu-verification-token",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.Feishu.VerificationTokenRef = ref
			},
		},
		{
			refName: "QQ_APP_SECRET",
			want:    "qq-app-secret",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.QQ.AppSecretRef = ref
			},
		},
		{
			refName: "DINGTALK_CLIENT_SECRET",
			want:    "dingtalk-client-secret",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.DingTalk.ClientSecretRef = ref
			},
		},
		{
			refName: "MATRIX_ACCESS_TOKEN",
			want:    "matrix-access-token",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.Matrix.AccessTokenRef = ref
			},
		},
		{
			refName: "MATRIX_CRYPTO_PASSPHRASE",
			want:    "matrix-passphrase",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.Matrix.CryptoPassphraseRef = ref
			},
		},
		{
			refName: "LINE_CHANNEL_SECRET",
			want:    "line-channel-secret",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.LINE.ChannelSecretRef = ref
			},
		},
		{
			refName: "LINE_ACCESS_TOKEN",
			want:    "line-access-token",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.LINE.ChannelAccessTokenRef = ref
			},
		},
		{
			refName: "WECOM_SECRET",
			want:    "wecom-secret",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.WeCom.SecretRef = ref
			},
		},
		{
			refName: "WEIXIN_TOKEN",
			want:    "weixin-token",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.Weixin.TokenRef = ref
			},
		},
		{
			refName: "IRC_PASSWORD",
			want:    "irc-password",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.IRC.PasswordRef = ref
			},
		},
		{
			refName: "IRC_NICKSERV_PASSWORD",
			want:    "irc-nickserv-password",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.IRC.NickServPasswordRef = ref
			},
		},
		{
			refName: "IRC_SASL_PASSWORD",
			want:    "irc-sasl-password",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Channels.IRC.SASLPasswordRef = ref
			},
		},
		{
			refName: "ELEVENLABS_API_KEY",
			want:    "elevenlabs-api-key",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Voice.ElevenLabsAPIKeyRef = ref
			},
		},
		{
			refName: "SKILLS_GITHUB_TOKEN",
			want:    "github-token",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Tools.Skills.Github.TokenRef = ref
			},
		},
		{
			refName: "CLAWHUB_AUTH_TOKEN",
			want:    "clawhub-token",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Tools.Skills.Registries.ClawHub.AuthTokenRef = ref
			},
		},
		{
			refName: "BRAVE_API_KEY",
			want:    "brave-api-key",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Tools.Web.Brave.APIKeyRef = ref
			},
		},
		{
			refName: "TAVILY_API_KEY",
			want:    "tavily-api-key",
			setOnCfg: func(cfg *config.Config, ref string) {
				cfg.Tools.Web.Tavily.APIKeyRef = ref
			},
		},
	}

	// Use a fixed 32-byte key (64 hex chars) as the test master key.
	// Set it on the parent test so sub-tests inherit it via t.Setenv scope.
	testKeyHex := hex.EncodeToString(make([]byte, 32)) // all-zero 32-byte key
	t.Setenv(credentials.EnvMasterKey, testKeyHex)

	for _, tc := range cases {
		t.Run(tc.refName, func(t *testing.T) {
			storePath := t.TempDir() + "/credentials.json"
			store := credentials.NewStore(storePath)
			if err := credentials.Unlock(store); err != nil {
				t.Fatalf("Unlock: %v", err)
			}
			if err := store.Set(tc.refName, tc.want); err != nil {
				t.Fatalf("store.Set(%q): %v", tc.refName, err)
			}

			cfg := &config.Config{}
			tc.setOnCfg(cfg, tc.refName)

			bundle, errs := credentials.ResolveBundle(cfg, store)
			if len(errs) > 0 {
				t.Errorf("ResolveBundle returned %d errors: %v", len(errs), errs)
			}

			got := bundle.GetString(tc.refName)
			if got != tc.want {
				t.Errorf("bundle.GetString(%q) = %q, want %q", tc.refName, got, tc.want)
			}
		})
	}
}

// TestSecretBundle_Get verifies the Get and GetString convenience methods.
func TestSecretBundle_Get(t *testing.T) {
	t.Parallel()

	b := credentials.SecretBundle{
		"PRESENT": "value",
		"EMPTY":   "",
	}

	t.Run("present ref", func(t *testing.T) {
		t.Parallel()
		v, ok := b.Get("PRESENT")
		if !ok || v != "value" {
			t.Errorf("Get(PRESENT) = (%q, %v), want (\"value\", true)", v, ok)
		}
	})

	t.Run("missing ref", func(t *testing.T) {
		t.Parallel()
		v, ok := b.Get("MISSING")
		if ok || v != "" {
			t.Errorf("Get(MISSING) = (%q, %v), want (\"\", false)", v, ok)
		}
	})

	t.Run("empty value ref", func(t *testing.T) {
		t.Parallel()
		v, ok := b.Get("EMPTY")
		if !ok || v != "" {
			t.Errorf("Get(EMPTY) = (%q, %v), want (\"\", true)", v, ok)
		}
	})

	t.Run("GetString missing returns empty", func(t *testing.T) {
		t.Parallel()
		v := b.GetString("MISSING")
		if v != "" {
			t.Errorf("GetString(MISSING) = %q, want \"\"", v)
		}
	})

	t.Run("nil bundle GetString is safe", func(t *testing.T) {
		t.Parallel()
		var nilBundle credentials.SecretBundle
		v := nilBundle.GetString("ANY")
		if v != "" {
			t.Errorf("nil bundle GetString = %q, want \"\"", v)
		}
	})
}
