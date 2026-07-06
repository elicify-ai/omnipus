package voice

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func TestDetectTranscriber(t *testing.T) {
	// Provider API keys are still injected via env (InjectFromConfig path).
	t.Setenv("TRANSCRIBER_TEST_GEMINI_KEY", "sk-gemini-model")
	t.Setenv("TRANSCRIBER_TEST_OPENAI_KEY", "sk-openai")
	t.Setenv("TRANSCRIBER_TEST_GROQ_KEY", "sk-groq-model")
	t.Setenv("TRANSCRIBER_TEST_AZURE_KEY", "sk-azure")
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
			name: "voice model name selects audio model transcriber",
			cfg: &config.Config{
				Voice: config.VoiceConfig{ModelName: "voice-gemini"},
				Providers: []*config.ModelConfig{
					{
						ModelName: "voice-gemini",
						Model:     "gemini/gemini-2.5-flash",
						APIKeyRef: "TRANSCRIBER_TEST_GEMINI_KEY",
					},
				},
			},
			wantName: "audio-model",
		},
		{
			name: "groq via model list",
			cfg: &config.Config{
				Providers: []*config.ModelConfig{
					{ModelName: "openai", Model: "openai/gpt-4o", APIKeyRef: "TRANSCRIBER_TEST_OPENAI_KEY"},
					{
						ModelName: "groq",
						Model:     "groq/llama-3.3-70b",
						APIKeyRef: "TRANSCRIBER_TEST_GROQ_KEY",
					},
				},
			},
			wantName: "groq",
		},
		{
			name: "voice model name selects non-gemini audio model transcriber",
			cfg: &config.Config{
				Voice: config.VoiceConfig{ModelName: "voice-openai-audio"},
				Providers: []*config.ModelConfig{
					{
						ModelName: "voice-openai-audio",
						Model:     "openai/gpt-4o-audio-preview",
						APIKeyRef: "TRANSCRIBER_TEST_OPENAI_KEY",
					},
				},
			},
			wantName: "audio-model",
		},
		{
			name: "voice model name selects azure audio model transcriber",
			cfg: &config.Config{
				Voice: config.VoiceConfig{ModelName: "voice-azure-audio"},
				Providers: []*config.ModelConfig{
					{
						ModelName: "voice-azure-audio",
						Model:     "azure/my-audio-deployment",
						APIKeyRef: "TRANSCRIBER_TEST_AZURE_KEY",
						APIBase:   "https://example.openai.azure.com",
					},
				},
			},
			wantName: "audio-model",
		},
		{
			name: "voice model name with non openai compatible protocol does not select audio model transcriber",
			cfg: &config.Config{
				Voice: config.VoiceConfig{ModelName: "voice-anthropic"},
				Providers: []*config.ModelConfig{
					{
						ModelName: "voice-anthropic",
						Model:     "anthropic/claude-sonnet-4.6",
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
					{Model: "groq/llama-3.3-70b"},
				},
			},
			wantNil: true,
		},
		{
			name: "provider key takes priority over model list",
			cfg: &config.Config{
				Providers: []*config.ModelConfig{
					{
						ModelName: "groq",
						Model:     "groq/llama-3.3-70b",
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
						ModelName: "other",
						Model:     "gemini/gemini-2.5-flash",
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
						ModelName: "groq",
						Model:     "groq/llama-3.3-70b",
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
					ModelName:           "voice-gemini",
					ElevenLabsAPIKeyRef: "TRANSCRIBER_TEST_ELEVENLABS_KEY",
				},
				Providers: []*config.ModelConfig{
					{
						ModelName: "voice-gemini",
						Model:     "gemini/gemini-2.5-flash",
						APIKeyRef: "TRANSCRIBER_TEST_GEMINI_KEY",
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
