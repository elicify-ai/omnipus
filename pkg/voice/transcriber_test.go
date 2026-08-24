package voice

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func TestDetectTranscriber(t *testing.T) {
	// Provider API keys are still injected via env (InjectFromConfig path).
	t.Setenv("TRANSCRIBER_TEST_ZAI_KEY", "sk-zai-model")
	t.Setenv("TRANSCRIBER_TEST_PROXY_KEY", "sk-proxy")
	t.Setenv("TRANSCRIBER_TEST_OPENAI_KEY", "sk-openai")
	t.Setenv("TRANSCRIBER_TEST_GROQ_KEY", "sk-groq-model")
	t.Setenv("TRANSCRIBER_TEST_ANTHROPIC_KEY", "sk-anthropic")
	t.Setenv("TRANSCRIBER_TEST_OTHER_KEY", "sk-other-model")

	// ElevenLabs key comes from the SecretBundle (no env injection).
	elevenLabsBundle := credentials.SecretBundle{
		"TRANSCRIBER_TEST_ELEVENLABS_KEY": "sk_elevenlabs_test",
	}
	emptyBundle := credentials.SecretBundle{}

	tests := []struct {
		name     string
		cfg      *config.Config
		bundle   credentials.SecretBundle
		wantNil  bool
		wantName string
	}{
		{
			name:    "no config",
			cfg:     &config.Config{},
			wantNil: true,
		},
		{
			// `voice.model_name` names a MODEL id, resolved through the
			// providers list (ADR-067 FR-034 — a row is the pair, and the
			// display alias is gone).
			name: "voice model name selects audio model transcriber",
			cfg: &config.Config{
				Voice: config.VoiceConfig{ModelName: "gpt-4o-audio-preview"},
				Providers: []*config.ModelConfig{
					{
						Provider:  "openai",
						Model:     "gpt-4o-audio-preview",
						APIKeyRef: "TRANSCRIBER_TEST_OPENAI_KEY",
					},
				},
			},
			wantName: "audio-model",
		},
		{
			name: "groq via model list",
			cfg: &config.Config{
				Providers: []*config.ModelConfig{
					{Provider: "openai", Model: "gpt-4.1", APIKeyRef: "TRANSCRIBER_TEST_OPENAI_KEY"},
					{
						Provider:  "groq",
						Model:     "llama-3.1-8b-instant",
						APIKeyRef: "TRANSCRIBER_TEST_GROQ_KEY",
					},
				},
			},
			wantName: "groq",
		},
		{
			name: "voice model name selects a second openai-compatible row",
			cfg: &config.Config{
				Voice: config.VoiceConfig{ModelName: "glm-4.5"},
				Providers: []*config.ModelConfig{
					{
						Provider:  "zai",
						Model:     "glm-4.5",
						APIKeyRef: "TRANSCRIBER_TEST_ZAI_KEY",
					},
				},
			},
			wantName: "audio-model",
		},
		{
			// A CUSTOM row — an operator's own OpenAI-compatible endpoint —
			// reaches the same transport, recognised by its `protocol`
			// field and not by any vendor list (ADR-067 FR-012).
			name: "custom openai-compatible row selects the audio model transcriber",
			cfg: &config.Config{
				Voice: config.VoiceConfig{ModelName: "my-audio-model"},
				Providers: []*config.ModelConfig{
					{
						Provider:  "my-proxy",
						Custom:    true,
						Protocol:  "openai-compatible",
						Model:     "my-audio-model",
						APIBase:   "https://llm.example/v1",
						APIKeyRef: "TRANSCRIBER_TEST_PROXY_KEY",
					},
				},
			},
			wantName: "audio-model",
		},
		{
			name: "voice model name on an anthropic row does not select the audio model transcriber",
			cfg: &config.Config{
				Voice: config.VoiceConfig{ModelName: "claude-sonnet-4-5"},
				Providers: []*config.ModelConfig{
					{
						Provider:  "anthropic",
						Model:     "claude-sonnet-4-5",
						APIKeyRef: "TRANSCRIBER_TEST_ANTHROPIC_KEY",
					},
				},
			},
			wantNil: true,
		},
		{
			name: "groq model list entry without key is skipped",
			cfg: &config.Config{
				Providers: []*config.ModelConfig{
					{Provider: "groq", Model: "llama-3.1-8b-instant"},
				},
			},
			wantNil: true,
		},
		{
			name: "provider key takes priority over model list",
			cfg: &config.Config{
				Providers: []*config.ModelConfig{
					{
						Provider:  "groq",
						Model:     "llama-3.1-8b-instant",
						APIKeyRef: "TRANSCRIBER_TEST_GROQ_KEY",
					},
				},
			},
			wantName: "groq",
		},
		{
			name: "missing voice model name config returns nil",
			cfg: &config.Config{
				Voice: config.VoiceConfig{ModelName: "missing"},
				Providers: []*config.ModelConfig{
					{
						Provider:  "google",
						Model:     "gemini-2.5-flash",
						APIKeyRef: "TRANSCRIBER_TEST_OTHER_KEY",
					},
				},
			},
			wantNil: true,
		},
		{
			name: "elevenlabs voice config key",
			cfg: &config.Config{
				Voice: config.VoiceConfig{ElevenLabsAPIKeyRef: "TRANSCRIBER_TEST_ELEVENLABS_KEY"},
			},
			bundle:   elevenLabsBundle,
			wantName: "elevenlabs",
		},
		{
			name: "elevenlabs takes priority over groq model list",
			cfg: &config.Config{
				Voice: config.VoiceConfig{ElevenLabsAPIKeyRef: "TRANSCRIBER_TEST_ELEVENLABS_KEY"},
				Providers: []*config.ModelConfig{
					{
						Provider:  "groq",
						Model:     "llama-3.1-8b-instant",
						APIKeyRef: "TRANSCRIBER_TEST_GROQ_KEY",
					},
				},
			},
			bundle:   elevenLabsBundle,
			wantName: "elevenlabs",
		},
		{
			name: "voice model name takes priority over elevenlabs",
			cfg: &config.Config{
				Voice: config.VoiceConfig{
					ModelName:           "gpt-4o-audio-preview",
					ElevenLabsAPIKeyRef: "TRANSCRIBER_TEST_ELEVENLABS_KEY",
				},
				Providers: []*config.ModelConfig{
					{
						Provider:  "openai",
						Model:     "gpt-4o-audio-preview",
						APIKeyRef: "TRANSCRIBER_TEST_OPENAI_KEY",
					},
				},
			},
			bundle:   elevenLabsBundle,
			wantName: "audio-model",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bundle := tc.bundle
			if bundle == nil {
				bundle = emptyBundle
			}
			tr := DetectTranscriber(tc.cfg, bundle)
			if tc.wantNil {
				if tr != nil {
					t.Errorf("DetectTranscriber() = %v, want nil", tr)
				}
				return
			}
			if tr == nil {
				t.Fatal("DetectTranscriber() = nil, want non-nil")
			}
			if got := tr.Name(); got != tc.wantName {
				t.Errorf("Name() = %q, want %q", got, tc.wantName)
			}
		})
	}
}
